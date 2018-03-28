// Copyright (c) Jeevanandam M. (https://github.com/jeevatkm)
// go-aah/router source code and usage is governed by a MIT style
// license that can be found in the LICENSE file.

// Package router provides routes implementation for aah framework application.
// Routes config format is `forge` config syntax (go-aah/config) which
// is similar to HOCON syntax aka typesafe config.
//
// aah framework router uses radix tree of
// https://github.com/julienschmidt/httprouter
package router

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"aahframework.org/ahttp.v0"
	"aahframework.org/config.v0"
	"aahframework.org/essentials.v0"
	"aahframework.org/log.v0"
)

const wildcardSubdomainPrefix = "*."

var (
	// HTTPMethodActionMap is default Controller Action name for corresponding
	// HTTP Method. If it's not provided in the route configuration.
	HTTPMethodActionMap = map[string]string{
		ahttp.MethodGet:     "Index",
		ahttp.MethodPost:    "Create",
		ahttp.MethodPut:     "Update",
		ahttp.MethodPatch:   "Update",
		ahttp.MethodDelete:  "Delete",
		ahttp.MethodOptions: "Options",
		ahttp.MethodHead:    "Head",
		ahttp.MethodTrace:   "Trace",
	}

	// ErrNoDomainRoutesConfigFound returned when routes config file not found or doesn't
	// have `domains { ... }` config information.
	ErrNoDomainRoutesConfigFound = errors.New("router: no domain routes config found")
)

type (
	// Router is used to register all application routes and finds the appropriate
	// route information for incoming request path.
	Router struct {
		Domains    map[string]*Domain
		configPath string
		config     *config.Config
		appCfg     *config.Config
	}

	// Domain is used to hold domain related routes and it's route configuration
	Domain struct {
		Name                  string
		Host                  string
		Port                  string
		IsSubDomain           bool
		MethodNotAllowed      bool
		RedirectTrailingSlash bool
		AutoOptions           bool
		DefaultAuth           string
		CORS                  *CORS
		CORSEnabled           bool
		trees                 map[string]*node
		routes                map[string]*Route
	}

	// Route holds the single route details.
	Route struct {
		Name            string
		Path            string
		Method          string
		Controller      string
		Action          string
		ParentName      string
		Auth            string
		MaxBodySize     int64
		IsAntiCSRFCheck bool
		CORS            *CORS

		// static route fields in-addition to above
		IsStatic bool
		Dir      string
		File     string
		ListDir  bool

		validationRules map[string]string
	}

	// PathParam is single URL path parameter (not a query string values)
	PathParam struct {
		Key   string
		Value string
	}

	// PathParams is a PathParam-slice, as returned by the route tree.
	PathParams []PathParam

	parentRouteInfo struct {
		ParentName  string
		PrefixPath  string
		Controller  string
		Auth        string
		CORS        *CORS
		CORSEnabled bool
	}
)

//‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Package methods
//___________________________________

// New method returns the Router instance.
func New(configPath string, appCfg *config.Config) *Router {
	return &Router{
		configPath: configPath,
		appCfg:     appCfg,
	}
}

// IsDefaultAction method is to identify given action name is defined by
// aah framework in absence of user configured route action name.
func IsDefaultAction(action string) bool {
	for _, a := range HTTPMethodActionMap {
		if a == action {
			return true
		}
	}
	return false
}

//‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Router methods
//___________________________________

// Load method loads a configuration from given file e.g. `routes.conf` and
// applies env profile override values if available.
func (r *Router) Load() (err error) {
	if !ess.IsFileExists(r.configPath) {
		return fmt.Errorf("router: configuration does not exists: %v", r.configPath)
	}

	r.config, err = config.LoadFile(r.configPath)
	if err != nil {
		return err
	}

	// apply aah.conf env variables
	if envRoutesValues, found := r.appCfg.GetSubConfig("routes"); found {
		log.Debug("env routes {...} values found, applying it")
		if err = r.config.Merge(envRoutesValues); err != nil {
			return fmt.Errorf("router: routes.conf: %s", err)
		}
	}

	err = r.processRoutesConfig()
	return
}

// FindDomain returns domain routes configuration based on http request
// otherwise nil.
func (r *Router) FindDomain(req *ahttp.Request) *Domain {
	host := strings.ToLower(req.Host)

	// Extact match of host value
	// for e.g.: sample.com:8080, www.sample.com:8080, admin.sample.com:8080
	if domain, found := r.Domains[host]; found {
		return domain
	}

	// Wildcard match of host value
	// for e.g.: router.conf value is `*.sample.com:8080` it matches
	// {subdomain}.sample.com
	if idx := strings.IndexByte(host, '.'); idx > 0 {
		if domain, found := r.Domains[wildcardSubdomainPrefix+host[idx+1:]]; found {
			return domain
		}
	}

	return nil
}

// RootDomain method returns the root domain registered in the routes.conf.
// For e.g.: sample.com, admin.sample.com, *.sample.com.
// Root Domain is `sample.com`.
func (r *Router) RootDomain() *Domain {
	for _, d := range r.Domains {
		if d.IsSubDomain {
			continue
		}
		return d
	}
	return nil
}

// DomainAddresses method returns domain addresses (host:port) from
// routes configuration.
func (r *Router) DomainAddresses() []string {
	var addresses []string

	for k := range r.Domains {
		addresses = append(addresses, k)
	}

	return addresses
}

// RegisteredActions method returns all the controller name and it's actions
// configured in the "routes.conf".
func (r *Router) RegisteredActions() map[string]map[string]uint8 {
	methods := map[string]map[string]uint8{}
	for _, d := range r.Domains {
		for _, route := range d.routes {
			if route.IsStatic {
				continue
			}

			addRegisteredAction(methods, route)
		}
	}

	return methods
}

func (r *Router) processRoutesConfig() (err error) {
	domains := r.config.KeysByPath("domains")
	if len(domains) == 0 {
		return ErrNoDomainRoutesConfigFound
	}

	_ = r.config.SetProfile("domains")

	// allocate for no. of domains
	r.Domains = make(map[string]*Domain, len(domains))
	log.Debugf("Domain count: %d", len(domains))

	for _, key := range domains {
		domainCfg, _ := r.config.GetSubConfig(key)

		// domain host name
		host, found := domainCfg.String("host")
		if !found {
			err = fmt.Errorf("'%v.host' key is missing", key)
			return
		}

		// Router takes the port-no in the order they found-
		//   1) routes.conf `domains.<domain-name>.port`
		//   2) aah.conf `server.port`
		//   3) 8080
		port := domainCfg.StringDefault("port",
			r.appCfg.StringDefault("server.port", "8080"))
		if port == "80" || port == "443" {
			port = ""
		}

		domain := &Domain{
			Name:                  domainCfg.StringDefault("name", key),
			Host:                  host,
			Port:                  port,
			IsSubDomain:           domainCfg.BoolDefault("subdomain", false),
			MethodNotAllowed:      domainCfg.BoolDefault("method_not_allowed", true),
			RedirectTrailingSlash: domainCfg.BoolDefault("redirect_trailing_slash", true),
			AutoOptions:           domainCfg.BoolDefault("auto_options", true),
			DefaultAuth:           domainCfg.StringDefault("default_auth", ""),
			CORSEnabled:           domainCfg.BoolDefault("cors.enable", false),
			trees:                 make(map[string]*node),
			routes:                make(map[string]*Route),
		}

		// Domain Level CORS configuration
		if domain.CORSEnabled {
			baseCORSCfg, _ := domainCfg.GetSubConfig("cors")
			domain.CORS = processBaseCORSSection(baseCORSCfg)
		}

		// Not Found route support is removed in aah v0.8 release,
		// in-favor of Centralized Error Handler.
		// Refer to https://docs.aahframework.org/centralized-error-handler.html

		// processing static routes
		if err = r.processStaticRoutes(domain, domainCfg); err != nil {
			return
		}

		// processing namespace routes
		if err = r.processRoutes(domain, domainCfg); err != nil {
			return
		}

		// add domain routes
		key := domain.key()
		log.Debugf("Domain: %s, routes found: %d", key, len(domain.routes))
		if log.IsLevelTrace() {
			// don't spend time here, process only if log level is trace
			// Static Files routes
			log.Trace("Static Files Routes")
			for _, dr := range domain.routes {
				if dr.IsStatic {
					log.Tracef("Route Name: %v, Path: %v, IsDir: %v, Dir: %v, ListDir: %v, IsFile: %v, File: %v",
						dr.Name, dr.Path, dr.IsDir(), dr.Dir, dr.ListDir, dr.IsFile(), dr.File)
				}
			}

			// Application routes
			log.Trace("Application Routes")
			for _, dr := range domain.routes {
				if dr.IsStatic {
					continue
				}
				parentInfo := ""
				if !ess.IsStrEmpty(dr.ParentName) {
					parentInfo = fmt.Sprintf("(parent: %s)", dr.ParentName)
				}
				log.Tracef("Route Name: %v %v, Path: %v, Method: %v, Controller: %v, Action: %v, Auth: %v, MaxBodySize: %v\nCORS: [%v]\nValidation Rules:%v\n",
					dr.Name, parentInfo, dr.Path, dr.Method, dr.Controller, dr.Action, dr.Auth, dr.MaxBodySize,
					dr.CORS, dr.validationRules)
			}
		}

		r.Domains[key] = domain

	} // End of domains

	r.config.ClearProfile()
	return
}

//‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Router unexpoted methods
//___________________________________

func (r *Router) processStaticRoutes(domain *Domain, domainCfg *config.Config) error {
	staticCfg, found := domainCfg.GetSubConfig("static")
	if !found {
		return nil
	}

	routes, err := parseStaticSection(staticCfg)
	if err != nil {
		return err
	}

	for idx := range routes {
		if err = domain.AddRoute(routes[idx]); err != nil {
			return err
		}
	}

	return nil
}

func (r *Router) processRoutes(domain *Domain, domainCfg *config.Config) error {
	routesCfg, found := domainCfg.GetSubConfig("routes")
	if !found {
		return nil
	}

	routes, err := parseRoutesSection(routesCfg, &parentRouteInfo{
		Auth:        domain.DefaultAuth,
		CORS:        domain.CORS,
		CORSEnabled: domainCfg.BoolDefault("cors.enable", false),
	})
	if err != nil {
		return err
	}

	for idx := range routes {
		if err = domain.AddRoute(routes[idx]); err != nil {
			return err
		}
	}
	return nil
}

//‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Domain methods
//___________________________________

// Lookup finds a route information, path parameters, redirect trailing slash
// indicator for given `ahttp.Request` by domain and request URI
// otherwise returns nil and false.
func (d *Domain) Lookup(req *ahttp.Request) (*Route, *PathParams, bool) {
	// HTTP method override support
	overrideMethod := req.Header.Get(ahttp.HeaderXHTTPMethodOverride)
	if !ess.IsStrEmpty(overrideMethod) && req.Method == ahttp.MethodPost {
		req.Method = overrideMethod
	}

	// get route tree for request method
	tree, found := d.lookupRouteTree(req)
	if !found {
		return nil, nil, false
	}

	routeName, pathParams, rts, err := tree.find(req.Path)
	if routeName != nil && err == nil {
		return d.routes[routeName.(string)], &pathParams, rts
	} else if rts { // possible Redirect Trailing Slash
		return nil, nil, rts
	}

	return nil, nil, false
}

// LookupByName method to find route information by route name.
func (d *Domain) LookupByName(name string) *Route {
	if route, found := d.routes[name]; found {
		return route
	}
	return nil
}

// AddRoute method to add the given route into domain routing tree.
func (d *Domain) AddRoute(route *Route) error {
	if ess.IsStrEmpty(route.Method) {
		return errors.New("router: method value is empty")
	}

	tree := d.trees[route.Method]
	if tree == nil {
		tree = new(node)
		d.trees[route.Method] = tree
	}

	if err := tree.add(route.Path, route.Name); err != nil {
		return err
	}

	d.routes[route.Name] = route
	return nil
}

// Allowed returns the header value for `Allow` otherwise empty string.
func (d *Domain) Allowed(requestMethod, path string) (allowed string) {
	if path == "*" { // server-wide
		for method := range d.trees {
			if method == ahttp.MethodOptions {
				continue
			}

			// add request method to list of allowed methods
			allowed = suffixCommaValue(allowed, method)
		}
	} else { // specific path
		for method := range d.trees {
			// Skip the requested method - we already tried this one
			if method == requestMethod || method == ahttp.MethodOptions {
				continue
			}

			value, _, _, _ := d.trees[method].find(path)
			if value != nil {
				// add request method to list of allowed methods
				allowed = suffixCommaValue(allowed, method)
			}
		}
	}

	return
}

// ReverseURLm composes reverse URL by route name and key-value pair arguments or
// zero argument for static URL. Additional key-values composed as URL query
// string. If error occurs then method logs an error and returns empty string.
func (d *Domain) ReverseURLm(routeName string, args map[string]interface{}) string {
	route, found := d.routes[routeName]
	if !found {
		log.Errorf("route name '%v' not found", routeName)
		return ""
	}

	argsLen := len(args)
	pathParamCnt := countParams(route.Path)
	if pathParamCnt == 0 && argsLen == 0 { // static URLs or no path params
		return route.Path
	}

	if argsLen < int(pathParamCnt) { // not enough arguments suppiled
		log.Errorf("not enough arguments, path: '%v' params count: %v, suppiled values count: %v",
			route.Path, pathParamCnt, argsLen)
		return ""
	}

	// compose URL with values
	reverseURL := "/"
	for _, segment := range strings.Split(route.Path, "/")[1:] {
		if ess.IsStrEmpty(segment) {
			continue
		}

		if segment[0] == paramByte || segment[0] == wildByte {
			argName := segment[1:]
			if arg, found := args[argName]; found {
				reverseURL = path.Join(reverseURL, fmt.Sprintf("%v", arg))
				delete(args, argName)
				continue
			}

			log.Errorf("'%v' param not found in given map", segment[1:])
			return ""
		}

		reverseURL = path.Join(reverseURL, segment)
	}

	// add remaining params into URL Query parameters, if any
	if len(args) > 0 {
		urlValues := url.Values{}

		for k, v := range args {
			urlValues.Add(k, fmt.Sprintf("%v", v))
		}

		reverseURL = fmt.Sprintf("%s?%s", reverseURL, urlValues.Encode())
	}

	rURL, err := url.Parse(reverseURL)
	if err != nil {
		log.Error(err)
		return ""
	}

	return rURL.String()
}

// ReverseURL method composes route reverse URL for given route and
// arguments based on index order. If error occurs then method logs
// an error and returns empty string.
func (d *Domain) ReverseURL(routeName string, args ...interface{}) string {
	route, found := d.routes[routeName]
	if !found {
		log.Errorf("route name '%v' not found", routeName)
		return ""
	}

	argsLen := len(args)
	pathParamCnt := countParams(route.Path)
	if pathParamCnt == 0 && argsLen == 0 { // static URLs or no path params
		return route.Path
	}

	// too many arguments
	if argsLen > int(pathParamCnt) {
		log.Errorf("too many arguments, path: '%v' params count: %v, suppiled values count: %v",
			route.Path, pathParamCnt, argsLen)
		return ""
	}

	// not enough arguments
	if argsLen < int(pathParamCnt) {
		log.Errorf("not enough arguments, path: '%v' params count: %v, suppiled values count: %v",
			route.Path, pathParamCnt, argsLen)
		return ""
	}

	var values []string
	for _, v := range args {
		values = append(values, fmt.Sprintf("%v", v))
	}

	// compose URL with values
	reverseURL := "/"
	idx := 0
	for _, segment := range strings.Split(route.Path, "/") {
		if ess.IsStrEmpty(segment) {
			continue
		}

		if segment[0] == paramByte || segment[0] == wildByte {
			reverseURL = path.Join(reverseURL, values[idx])
			idx++
			continue
		}

		reverseURL = path.Join(reverseURL, segment)
	}

	rURL, err := url.Parse(reverseURL)
	if err != nil {
		log.Error(err)
		return ""
	}

	return rURL.String()
}

func (d *Domain) key() string {
	if ess.IsStrEmpty(d.Port) {
		return strings.ToLower(d.Host)
	}
	return strings.ToLower(d.Host + ":" + d.Port)
}

func (d *Domain) lookupRouteTree(req *ahttp.Request) (*node, bool) {
	// get route tree for request method
	if tree, found := d.trees[req.Method]; found {
		return tree, true
	}

	// get route tree for CORS access control method
	if req.Method == ahttp.MethodOptions && d.CORSEnabled {
		if tree, found := d.trees[req.Header.Get(ahttp.HeaderAccessControlRequestMethod)]; found {
			return tree, true
		}
	}

	return nil, false
}

//‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Route methods
//___________________________________

// IsDir method returns true if serving directory otherwise false.
func (r *Route) IsDir() bool {
	return !ess.IsStrEmpty(r.Dir) && ess.IsStrEmpty(r.File)
}

// IsFile method returns true if serving single file otherwise false.
func (r *Route) IsFile() bool {
	return !ess.IsStrEmpty(r.File)
}

// ValidationRule methdo returns `validation rule, true` if exists for path param
// otherwise `"", false`
func (r *Route) ValidationRule(name string) (string, bool) {
	rules, found := r.validationRules[name]
	return rules, found
}

//‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Path Param methods
//___________________________________

// Get method returns the value of the first Path Param which key matches the
// given name. Otherwise an empty string is returned.
func (pp PathParams) Get(name string) string {
	for _, p := range pp {
		if p.Key == name {
			return p.Value
		}
	}
	return ""
}

// Len method returns number key values in the path params
func (pp PathParams) Len() int {
	return len(pp)
}

//‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Unexported methods
//___________________________________

func addRegisteredAction(methods map[string]map[string]uint8, route *Route) {
	if controller, found := methods[route.Controller]; found {
		controller[route.Action] = 1
	} else {
		methods[route.Controller] = map[string]uint8{route.Action: 1}
	}
}

func parseRoutesSection(cfg *config.Config, routeInfo *parentRouteInfo) (routes []*Route, err error) {
	for _, routeName := range cfg.Keys() {
		// getting 'path'
		routePath, found := cfg.String(routeName + ".path")
		if !found && ess.IsStrEmpty(routeInfo.PrefixPath) {
			err = fmt.Errorf("'%v.path' key is missing", routeName)
			return
		}

		// path must begin with '/'
		if !ess.IsStrEmpty(routePath) && routePath[0] != slashByte {
			err = fmt.Errorf("'%v.path' [%v], path must begin with '/'", routeName, routePath)
			return
		}

		routePath = path.Join(routeInfo.PrefixPath, routePath)

		// Split validation rules from path params
		pathParamRules := make(map[string]string)
		actualRoutePath := "/"
		for _, seg := range strings.Split(routePath, "/")[1:] {
			if len(seg) == 0 {
				continue
			}

			if seg[0] == paramByte || seg[0] == wildByte {
				param, rules, exists, valid := checkValidationRule(seg)
				if exists {
					if valid {
						pathParamRules[param[1:]] = rules
					} else {
						err = fmt.Errorf("'%v.path' has invalid validation rule '%v'", routeName, routePath)
						return
					}
				}

				actualRoutePath = path.Join(actualRoutePath, param)
			} else {
				actualRoutePath = path.Join(actualRoutePath, seg)
			}
		}

		// check child routes exists
		notToSkip := true
		if cfg.IsExists(routeName + ".routes") {
			if !cfg.IsExists(routeName+".action") || !cfg.IsExists(routeName+".controller") {
				notToSkip = false
			}
		}

		// getting 'method', default to GET, if method not found
		routeMethod := strings.ToUpper(cfg.StringDefault(routeName+".method", ahttp.MethodGet))

		// getting 'controller'
		routeController := cfg.StringDefault(routeName+".controller", routeInfo.Controller)
		if ess.IsStrEmpty(routeController) && notToSkip {
			err = fmt.Errorf("'%v.controller' key is missing", routeName)
			return
		}

		// getting 'action', if not found it will default to `HTTPMethodActionMap`
		// based on `routeMethod`. For multiple HTTP method mapping scenario,
		// this is required attribute.
		routeAction := cfg.StringDefault(routeName+".action", findActionByHTTPMethod(routeMethod))
		if ess.IsStrEmpty(routeAction) && notToSkip {
			err = fmt.Errorf("'%v.action' key is missing, it seems to be multiple HTTP methods", routeName)
			return
		}

		// getting route authentication scheme name
		routeAuth := cfg.StringDefault(routeName+".auth", routeInfo.Auth)

		// getting route max body size, GitHub go-aah/aah#83
		routeMaxBodySize, er := ess.StrToBytes(cfg.StringDefault(routeName+".max_body_size", "0kb"))
		if er != nil {
			log.Warnf("'%v.max_body_size' value is not a valid size unit, fallback to global limit", routeName)
		}

		// getting Anti-CSRF check value, GitHub go-aah/aah#115
		routeAntiCSRFCheck := cfg.BoolDefault(routeName+".anti_csrf_check", true)

		// CORS
		var cors *CORS
		if routeInfo.CORSEnabled {
			if corsCfg, found := cfg.GetSubConfig(routeName + ".cors"); found {
				if corsCfg.BoolDefault("enable", true) {
					cors = processCORSSection(corsCfg, routeInfo.CORS)
				}
			} else {
				cors = routeInfo.CORS
			}
		}

		if notToSkip {
			for _, m := range strings.Split(routeMethod, ",") {
				routes = append(routes, &Route{
					Name:            routeName,
					Path:            actualRoutePath,
					Method:          strings.TrimSpace(m),
					Controller:      routeController,
					Action:          routeAction,
					ParentName:      routeInfo.ParentName,
					Auth:            routeAuth,
					MaxBodySize:     routeMaxBodySize,
					IsAntiCSRFCheck: routeAntiCSRFCheck,
					CORS:            cors,
					validationRules: pathParamRules,
				})
			}
		}

		// loading child routes
		if childRoutes, found := cfg.GetSubConfig(routeName + ".routes"); found {
			croutes, er := parseRoutesSection(childRoutes, &parentRouteInfo{
				ParentName:  routeName,
				PrefixPath:  routePath,
				Controller:  routeController,
				Auth:        routeAuth,
				CORS:        cors,
				CORSEnabled: routeInfo.CORSEnabled,
			})
			if er != nil {
				err = er
				return
			}

			routes = append(routes, croutes...)
		}
	}

	return
}

func parseStaticSection(cfg *config.Config) (routes []*Route, err error) {
	for _, routeName := range cfg.Keys() {
		route := &Route{Name: routeName, Method: ahttp.MethodGet, IsStatic: true}

		// getting 'path'
		routePath, found := cfg.String(routeName + ".path")
		if !found {
			err = fmt.Errorf("'static.%v.path' key is missing", routeName)
			return
		}

		// path must begin with '/'
		if routePath[0] != slashByte {
			err = fmt.Errorf("'static.%v.path' [%v], path must begin with '/'", routeName, routePath)
			return
		}

		if strings.Contains(routePath, ":") || strings.Contains(routePath, "*") {
			err = fmt.Errorf("'static.%v.path' parameters can not be used with static", routeName)
			return
		}

		route.Path = path.Clean(routePath)

		routeDir, dirFound := cfg.String(routeName + ".dir")
		routeFile, fileFound := cfg.String(routeName + ".file")
		if dirFound && fileFound {
			err = fmt.Errorf("'static.%v.dir' & 'static.%v.file' key(s) cannot be used together", routeName, routeName)
			return
		}

		if !dirFound && !fileFound {
			err = fmt.Errorf("either 'static.%v.dir' or 'static.%v.file' key have to be present", routeName, routeName)
			return
		}

		if dirFound {
			route.Path = path.Join(route.Path, "*filepath")
		}

		if fileFound {
			// GitHub #141 - for a file mapping
			//  - 'base_dir' attribute value is not provided and
			//  - file 'path' value relative path
			// then use 'public_assets.dir' as a default value.
			if dir, found := cfg.String(routeName + ".base_dir"); found {
				routeDir = dir
			} else if routeFile[0] != slashByte { // relative file path mapping
				if dir, found := cfg.String("public_assets.dir"); found {
					routeDir = dir
				} else {
					err = fmt.Errorf("'static.%v.base_dir' value is missing", routeName)
					return
				}
			}
		}

		route.Dir = routeDir
		route.File = routeFile
		route.ListDir = cfg.BoolDefault(routeName+".list", false)

		routes = append(routes, route)
	}

	return
}
