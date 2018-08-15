// Copyright (c) Jeevanandam M. (https://github.com/jeevatkm)
// aahframework.org/router source code and usage is governed by a MIT style
// license that can be found in the LICENSE file.

package router

import (
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aahframework.org/ahttp.v0"
	"aahframework.org/config.v0"
	"aahframework.org/essentials.v0"
	"aahframework.org/log.v0"
	"aahframework.org/security.v0"
	"aahframework.org/security.v0/scheme"
	"aahframework.org/test.v0/assert"
	"aahframework.org/valpar.v0"
	"aahframework.org/vfs.v0"
)

//‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Test Path Params methods
//___________________________________

func TestRouterPathParamGet(t *testing.T) {
	pathParameters := ahttp.PathParams{
		"dir":      "js",
		"filepath": "/inc/framework.js",
	}

	fp := pathParameters.Get("filepath")
	assert.Equal(t, "/inc/framework.js", fp)

	notfound := pathParameters.Get("notfound")
	assert.Equal(t, "", notfound)
}

//‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Test Router methods
//___________________________________

func TestRouterLoadConfiguration(t *testing.T) {
	router, err := createRouter("routes.conf")
	assert.FailNowOnError(t, err, "")

	// After loading just couple of assertion
	reqCancelBooking1 := createHTTPRequest("localhost:8080", "/hotels/12345/cancel")
	reqCancelBooking1.Method = ahttp.MethodPost
	domain := router.Lookup(reqCancelBooking1.Host)
	route, pathParam, rts := domain.Lookup(reqCancelBooking1)
	assert.Equal(t, "cancel_booking", route.Name)
	assert.Equal(t, "12345", pathParam.Get("id"))
	assert.False(t, rts)
	assert.Equal(t, 1, len(pathParam))

	// Lookup by name
	cancelBooking := domain.LookupByName("cancel_booking")
	assert.Equal(t, "hotels_group", cancelBooking.ParentName)
	assert.Equal(t, "cancel_booking", cancelBooking.Name)
	assert.Equal(t, "Hotel", cancelBooking.Target)
	assert.Equal(t, "POST", cancelBooking.Method)

	routeNotFound := domain.LookupByName("cancel_booking_not_found")
	assert.Nil(t, routeNotFound)

	// Method missing
	err = domain.AddRoute(&Route{
		Name: "MethodMissing",
		Path: "/:user/test",
	})
	assert.Equal(t, "router: method value is empty", err.Error())

	err = domain.AddRoute(&Route{
		Name:   "ErrorRoute",
		Path:   "/hotels/:user/test",
		Method: "GET",
	})
	assert.Equal(t, errors.New("aah/router: parameter based edge already exists[/hotels/:id...] new[/hotels/:user...]"), err)

	domain.Port = ""
	domain.inferKey()
	assert.Equal(t, "localhost", domain.Key)
}

func TestRouterWildcardSubdomain(t *testing.T) {
	router, err := createRouter("routes.conf")
	assert.FailNowOnError(t, err, "")

	reqCancelBooking := createHTTPRequest("localhost:8080", "/hotels/12345/cancel")
	reqCancelBooking.Method = ahttp.MethodPost
	domain := router.Lookup(reqCancelBooking.Host)
	assert.Equal(t, "localhost", domain.Host)

	rootDomain := router.RootDomain()
	assert.Equal(t, "localhost", rootDomain.Host)
	assert.Equal(t, "8080", rootDomain.Port)

	reqWildcardUsername1 := createHTTPRequest("username1.localhost:8080", "/")
	reqWildcardUsername1.Method = ahttp.MethodGet
	domain = router.Lookup(reqWildcardUsername1.Host)
	assert.Equal(t, "*.localhost", domain.Host)
	assert.Equal(t, "8080", domain.Port)

	route1, _, rts1 := domain.Lookup(reqWildcardUsername1)
	assert.False(t, rts1)
	assert.Equal(t, "index", route1.Name)
	assert.Equal(t, "wildcard/AppController", route1.Target)
	assert.Equal(t, "/", route1.Path)

	reqWildcardUsername2 := createHTTPRequest("username2.localhost:8080", "/")
	reqWildcardUsername2.Method = ahttp.MethodGet
	domain = router.Lookup(reqWildcardUsername2.Host)
	assert.Equal(t, "*.localhost", domain.Host)
	assert.Equal(t, "8080", domain.Port)

	route2, _, rts2 := domain.Lookup(reqWildcardUsername2)
	assert.False(t, rts2)
	assert.Equal(t, "index", route2.Name)
	assert.Equal(t, "wildcard/AppController", route2.Target)
	assert.Equal(t, "/", route2.Path)
}

func TestRouterStaticLoadConfiguration(t *testing.T) {
	router, err := createRouter("routes.conf")
	assert.FailNowOnError(t, err, "")

	// After loading just couple assertion for static

	// /favicon.ico
	req1 := createHTTPRequest("localhost:8080", "/favicon.ico")
	req1.Method = ahttp.MethodGet
	domain := router.Lookup(req1.Host)
	route, pathParam, rts := domain.Lookup(req1)
	assert.Nil(t, pathParam)
	assert.False(t, rts)
	assert.True(t, route.IsStatic)
	assert.Equal(t, "/public/img/favicon.png", route.File)
	assert.Equal(t, "", route.Dir)
	assert.False(t, route.IsDir())
	assert.True(t, route.IsFile())

	// /static/img/aahframework.png
	req2 := createHTTPRequest("localhost:8080", "/static/img/aahframework.png")
	req2.Method = ahttp.MethodGet
	domain = router.Lookup(req2.Host)
	route, pathParam, rts = domain.Lookup(req2)
	assert.NotNil(t, pathParam)
	assert.False(t, rts)
	assert.True(t, route.IsStatic)
	assert.Equal(t, "/public", route.Dir)
	assert.Equal(t, "img/aahframework.png", pathParam.Get("filepath"))
	assert.Equal(t, "", route.File)
	assert.True(t, route.IsDir())
	assert.False(t, route.IsFile())

	// static
	staticDirReq := createHTTPRequest("localhost:8080", "/static")
	staticDirReq.Method = ahttp.MethodGet
	route, params, rts := domain.Lookup(staticDirReq)
	assert.True(t, rts)
	assert.Nil(t, route)
	assert.Nil(t, params)

	notfoundMethod := createHTTPRequest("sample.localhost:8080", "/static")
	notfoundMethod.Method = ahttp.MethodOptions
	domain = router.Lookup(notfoundMethod.Host)
	route, params, rts = domain.Lookup(notfoundMethod)
	assert.False(t, rts)
	assert.Nil(t, route)
	assert.Nil(t, params)
}

func TestRouterErrorLoadConfiguration(t *testing.T) {
	router, err := createRouter("routes-error.conf")
	assert.NotNilf(t, err, "expected error loading '%v'", "routes-error.conf")
	assert.Nil(t, router)
	assert.True(t, strings.HasPrefix(err.Error(), "syntax error line"))
}

func TestRouterErrorHostLoadConfiguration(t *testing.T) {
	router, err := createRouter("routes-no-hostname.conf")
	assert.NotNilf(t, err, "expected error loading '%v'", "routes-no-hostname.conf")
	assert.Nil(t, router)
	assert.Equal(t, "'localhost.host' key is missing", err.Error())
}

func TestRouterErrorPathLoadConfiguration(t *testing.T) {
	router, err := createRouter("routes-path-error.conf")
	assert.NotNilf(t, err, "expected error loading '%v'", "routes-path-error.conf")
	assert.Nil(t, router)
	assert.Equal(t, "'app_index.path' key is missing", err.Error())
}

func TestRouterErrorControllerLoadConfiguration(t *testing.T) {
	router, err := createRouter("routes-controller-error.conf")
	assert.NotNilf(t, err, "expected error loading '%v'", "routes-controller-error.conf")
	assert.Nil(t, router)
	assert.Equal(t, "'app_index.controller' or 'app_index.websocket' key is missing", err.Error())
}

func TestRouterErrorStaticPathLoadConfiguration(t *testing.T) {
	router, err := createRouter("routes-static-path-error.conf")
	assert.NotNilf(t, err, "expected error loading '%v'", "routes-static-path-error.conf")
	assert.Nil(t, router)
	assert.Equal(t, "'static.public.path' key is missing", err.Error())
}

func TestRouterErrorStaticPathPatternLoadConfiguration(t *testing.T) {
	router, err := createRouter("routes-static-path-pattern-error.conf")
	assert.NotNilf(t, err, "expected error loading '%v'", "routes-static-path-pattern-error.conf")
	assert.Nil(t, router)
	assert.Equal(t, "'static.public.path' parameters can not be used with static", err.Error())
}

func TestRouterErrorStaticDirFileLoadConfiguration(t *testing.T) {
	router, err := createRouter("routes-static-dir-file-error.conf")
	assert.NotNilf(t, err, "expected error loading '%v'", "routes-static-dir-file-error.conf")
	assert.Nil(t, router)
	assert.Equal(t, "'static.public.dir' & 'static.public.file' key(s) cannot be used together", err.Error())
}

func TestRouterErrorStaticNoDirFileLoadConfiguration(t *testing.T) {
	router, err := createRouter("routes-static-no-dir-file-error.conf")
	assert.NotNilf(t, err, "expected error loading '%v'", "routes-static-no-dir-file-error.conf")
	assert.Nil(t, router)
	assert.Equal(t, "either 'static.public.dir' or 'static.public.file' key have to be present", err.Error())
}

func TestRouterErrorStaticPathBeginSlashLoadConfiguration(t *testing.T) {
	router, err := createRouter("routes-static-path-slash-error.conf")
	assert.NotNilf(t, err, "expected error loading '%v'", "routes-static-path-slash-error.conf")
	assert.Nil(t, router)
	assert.Equal(t, "'static.public.path' [static], path must begin with '/'", err.Error())
}

func TestRouterNoDomainRoutesFound(t *testing.T) {
	router, err := createRouter("routes-no-domains.conf")
	assert.Equal(t, ErrNoDomainRoutesConfigFound, err)
	assert.Nil(t, router)
}

func TestRouterDomainConfig(t *testing.T) {
	router, err := createRouter("routes.conf")
	assert.FailNowOnError(t, err, "")

	domain := router.FindDomain(ahttp.AcquireRequest(createHTTPRequest("localhost:8080", "/")))
	assert.NotNil(t, domain)

	domain = router.FindDomain(ahttp.AcquireRequest(createHTTPRequest("www.aahframework.org", "/")))
	assert.Nil(t, domain)
}

func TestRouterDomainAddresses(t *testing.T) {
	router, err := createRouter("routes.conf")
	assert.FailNowOnError(t, err, "")

	addresses := router.DomainAddresses()
	assert.True(t, len(addresses) == 2)
}

func TestRouterRegisteredActions(t *testing.T) {
	router, err := createRouter("routes.conf")
	assert.FailNowOnError(t, err, "")

	methods := router.RegisteredActions()
	assert.NotNil(t, methods)
	assert.Equal(t, 3, len(methods))
}

func TestRouterIsDefaultAction(t *testing.T) {
	v1 := IsDefaultAction("Index")
	assert.True(t, v1)

	v2 := IsDefaultAction("Head")
	assert.True(t, v2)

	v3 := IsDefaultAction("Show")
	assert.False(t, v3)
}

//‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Test Domain methods
//___________________________________

func TestRouterDomainAllowed(t *testing.T) {
	router, err := createRouter("routes.conf")
	assert.FailNowOnError(t, err, "")

	req := createHTTPRequest("localhost:8080", "/login")
	domain := router.FindDomain(ahttp.AcquireRequest(req))
	allow := domain.Allowed(ahttp.MethodGet, "/login")
	assert.NotNil(t, allow)
	assert.False(t, ess.IsStrEmpty(allow))

	domain = router.FindDomain(ahttp.AcquireRequest(req))
	allow = domain.Allowed(ahttp.MethodPost, "*")
	assert.NotNil(t, allow)
	assert.True(t, strings.Contains(allow, ahttp.MethodPost))
	assert.True(t, strings.Contains(allow, ahttp.MethodGet))

	// domain not exists
	reqNotExists := createHTTPRequest("notexists:8080", "/")
	domain = router.Lookup(reqNotExists.Host)
	assert.Nil(t, domain)
}

func TestRouterDomainRouteURL(t *testing.T) {
	router, err := createRouter("routes.conf")
	assert.FailNowOnError(t, err, "")

	req := createHTTPRequest("localhost:8080", "/")
	domain := router.Lookup(req.Host)

	// route name not exists
	emptyURL := domain.RouteURLNamedArgs("not_exists_routename", map[string]interface{}{})
	assert.Equal(t, "", emptyURL)
	emptyURL = domain.RouteURL("not_exists_routename")
	assert.Equal(t, "", emptyURL)

	// not enough arguments
	emptyURL = domain.RouteURLNamedArgs("book_hotels", map[string]interface{}{})
	assert.Equal(t, "", emptyURL)
	emptyURL = domain.RouteURL("book_hotels")
	assert.Equal(t, "", emptyURL)

	// incorrect key name scenario
	emptyURL = domain.RouteURLNamedArgs("book_hotels", map[string]interface{}{
		"idvalue": "12345678",
	})
	assert.Equal(t, "", emptyURL)

	// index route
	indexURL := domain.RouteURLNamedArgs("app_index", map[string]interface{}{})
	assert.Equal(t, "/", indexURL)
	indexURL = domain.RouteURL("app_index")
	assert.Equal(t, "/", indexURL)

	// static URL
	loginURL := domain.RouteURLNamedArgs("login", map[string]interface{}{})
	assert.Equal(t, "/login", loginURL)
	loginURL = domain.RouteURL("login")
	assert.Equal(t, "/login", loginURL)

	// success scenario
	bookingURL := domain.RouteURLNamedArgs("book_hotels", map[string]interface{}{
		"id": "12345678",
	})
	assert.Equal(t, "/hotels/12345678/booking", bookingURL)
	bookingURL = domain.RouteURL("book_hotels", 12345678)
	assert.Equal(t, "/hotels/12345678/booking", bookingURL)

	bookingURL = domain.RouteURLNamedArgs("book_hotels", map[string]interface{}{
		"id":     "12345678",
		"param1": "param1value",
		"param2": "param2value",
	})
	assert.Equal(t, "/hotels/12345678/booking?param1=param1value&param2=param2value", bookingURL)

	bookingURL = domain.RouteURL("book_hotels", 12345678, "param1value", "param2value")
	assert.Equal(t, "", bookingURL)
}

func TestRouterDomainAddRoute(t *testing.T) {
	domain := &Domain{
		Host:   "aahframework.org",
		trees:  make(map[string]*tree),
		routes: make(map[string]*Route),
	}

	route1 := &Route{
		Name:   "route1",
		Path:   "/info/:user/project/:project",
		Method: "GET",
		Target: "Info",
		Action: "ShowProject",
	}
	err := domain.AddRoute(route1)
	assert.FailNowOnError(t, err, "unexpected error")

	route2 := &Route{
		Name:   "index",
		Path:   "/",
		Method: "GET",
		Target: "App",
		Action: "Index",
	}
	err = domain.AddRoute(route2)
	assert.FailNowOnError(t, err, "unexpected error")

	routeError := &Route{
		Name:   "route_error",
		Path:   "/",
		Method: "GET",
		Target: "App",
		Action: "Index",
	}
	err = domain.AddRoute(routeError)
	assert.Equal(t, errNodeExists, err)
}

func TestRouterConfigNotExists(t *testing.T) {
	router, err := createRouter("routes-not-exists.conf")
	assert.NotNil(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "router: configuration does not exists"))
	assert.Nil(t, router)
}

func TestRouterNamespaceConfig(t *testing.T) {
	router, err := createRouter("routes-namespace.conf")
	assert.FailNowOnError(t, err, "")

	routes := router.Lookup("localhost:8080").routes
	assert.NotNil(t, routes)
	assert.Equal(t, 5, len(routes))
	assert.Equal(t, "/v1/users", routes["create_user"].Path)
	assert.Equal(t, "POST", routes["create_user"].Method)
	assert.Equal(t, "form", routes["create_user"].Auth)
	assert.Equal(t, "/v1/users/:id/settings", routes["disable_user"].Path)
	assert.Equal(t, "GET", routes["disable_user"].Method)
	assert.Equal(t, "form", routes["disable_user"].Auth)

	// Error
	_, err = createRouter("routes-namespace-action-error.conf")
	assert.NotNil(t, err)
	assert.Equal(t, "'list_users.action' key is missing or it seems to be multiple HTTP methods", err.Error())
}

func TestRouterNamespaceSimplifiedConfig(t *testing.T) {
	router, err := createRouter("routes-simplified.conf")
	assert.FailNowOnError(t, err, "")

	routes := router.Lookup("localhost:8080").routes
	assert.NotNil(t, routes)
	assert.Equal(t, 4, len(routes))

	// show_basket
	assert.Equal(t, "/baskets/:id", routes["show_basket"].Path)
	assert.Equal(t, "GET", routes["show_basket"].Method)
	assert.Equal(t, "anonymous", routes["show_basket"].Auth)
	assert.Equal(t, "BasketController", routes["show_basket"].Target)

	// create_basket
	assert.Equal(t, "/baskets", routes["create_basket"].Path)
	assert.Equal(t, "POST", routes["create_basket"].Method)
	assert.Equal(t, "form_auth", routes["create_basket"].Auth)
	assert.Equal(t, "BasketController", routes["create_basket"].Target)
}

func TestRouterNamespaceSimplified2Config(t *testing.T) {
	router, err := createRouter("routes-simplified-2.conf")
	assert.FailNowOnError(t, err, "")

	routes := router.Lookup("localhost:8080").routes
	assert.NotNil(t, routes)
	assert.Equal(t, 8, len(routes))

	for _, v := range strings.Fields("list_users delete_user get_user get_user_settings update_user update_user_settings create_user") {
		if _, found := routes[v]; !found {
			assert.True(t, found)
		}
	}

	userSettingsRoute := routes["get_user_settings"]
	assert.Equal(t, 1, len(userSettingsRoute.Constraints))
	constraint, found := userSettingsRoute.Constraints["id"]
	assert.True(t, found)
	assert.Equal(t, "gt=1,lt=10", constraint)

	// Error
	_, err = createRouter("routes-simplified-2-error.conf")
	assert.NotNil(t, err)
	assert.Equal(t, errors.New(`'routes.path' has invalid contraint in path => '/v1/users/:id  gt=1,lt=10]' (param => ':id  gt=1,lt=10]')`), err)
}

func TestRouterStaticSectionBaseDirForFilePaths(t *testing.T) {
	router, err := createRouter("routes-static.conf")
	assert.FailNowOnError(t, err, "")

	// Assertion
	routes := router.Lookup("localhost:8080").routes
	assert.NotNil(t, routes)
	assert.Equal(t, 5, len(routes))

	faviconRoute := routes["favicon"]
	assert.False(t, faviconRoute.IsDir())
	assert.True(t, faviconRoute.IsFile())
	assert.Equal(t, "assets", faviconRoute.Dir)
	assert.Equal(t, "img/favicon.png", faviconRoute.File)

	robotTxtRoute := routes["robots_txt"]
	assert.False(t, robotTxtRoute.IsDir())
	assert.True(t, robotTxtRoute.IsFile())
	assert.Equal(t, "static", robotTxtRoute.Dir)
	assert.Equal(t, "robots.txt", robotTxtRoute.File)

	// ERROR missing values
	_, err = createRouter("routes-static-base-dir-missing.conf")
	assert.NotNil(t, err)
	assert.Equal(t, "'static.favicon.base_dir' value is missing", err.Error())
}

func TestRouterWebSocketConfig(t *testing.T) {
	router, err := createRouter("routes-websocket.conf")
	assert.FailNowOnError(t, err, "")

	routes := router.Lookup("localhost:8080").routes
	assert.NotNil(t, routes)
	assert.Equal(t, 10, len(routes))

	// WebSocket
	assert.Equal(t, "/ws/binary", routes["ws_binary"].Path)
	assert.Equal(t, "WS", routes["ws_binary"].Method)
	assert.Equal(t, "anonymous", routes["ws_binary"].Auth)
	assert.Equal(t, "TestWebSocket", routes["ws_binary"].Target)
	assert.Equal(t, "Binary", routes["ws_binary"].Action)

	assert.Equal(t, "/ws/text", routes["ws_text"].Path)
	assert.Equal(t, "WS", routes["ws_text"].Method)
	assert.Equal(t, "anonymous", routes["ws_text"].Auth)
	assert.Equal(t, "TestWebSocket", routes["ws_text"].Target)
	assert.Equal(t, "Text", routes["ws_text"].Action)

	methods := router.RegisteredWSActions()
	assert.NotNil(t, methods)
	assert.Equal(t, 1, len(methods))
}

func TestRoutePathConstraints(t *testing.T) {
	testcases := []struct {
		label, name, path, actualpath string
		constraints                   map[string]string
		values                        []map[string]string
		err                           error
	}{
		{
			label:      "no path parameter",
			name:       "products",
			path:       "/api/v1/products",
			actualpath: "/api/v1/products",
		},
		{
			label:      "path parameter with no constraints",
			name:       "products",
			path:       "/api/v1/products/:id",
			actualpath: "/api/v1/products/:id",
		},
		{
			label:      "path parameter with constraints",
			name:       "products",
			path:       "/api/v1/products/:id[uuid]/colors/:color[oneof=blue green red,alpha]",
			actualpath: "/api/v1/products/:id/colors/:color",
			constraints: map[string]string{
				"id":    "uuid",
				"color": "oneof=blue green red,alpha",
			},
			values: []map[string]string{
				{
					"id":    "5de80bf1-b2c7-4c6e-b0bc-e47758b7d817",
					"color": "green",
				},
				{
					"id": "dshkjfdgf",
				},
				{
					"color": "dksjhfd",
				},
			},
		},
	}

	// validate := validator.New()
	for _, tc := range testcases {
		t.Run(tc.label, func(t *testing.T) {
			routePath, constraints, err := parseRouteConstraints(tc.name, tc.path)
			assert.Equal(t, tc.err, err)
			assert.Equal(t, tc.actualpath, routePath)
			assert.Equal(t, tc.constraints, constraints)

			if len(constraints) > 0 {
				for ek, ev := range tc.constraints {
					if cv, found := constraints[ek]; found {
						assert.Equal(t, ev, cv)
					}
				}

				if len(tc.values) > 0 {
					for _, vs := range tc.values {
						if errs := valpar.ValidateValues(vs, constraints); len(errs) > 0 {
							f := errs[0].Field
							assert.Equal(t, tc.constraints[f], constraints[f])
						}
					}
				}
			}

		})
	}
}

func TestMiscRouter(t *testing.T) {
	r, err := NewWithApp(nil, "configPath")
	assert.NotNil(t, err)
	assert.Equal(t, "router: not a valid aah application instance", err.Error())
	assert.Nil(t, r)

	r = New("configPath", nil)
	assert.NotNil(t, r)
	assert.Nil(t, r.config)

	addSlashPrefix("welcome")
}

type app struct {
	cfg *config.Config
	l   log.Loggerer
	fs  *vfs.VFS
	sec *security.Manager
}

func (a *app) Config() *config.Config             { return a.cfg }
func (a *app) Log() log.Loggerer                  { return a.l }
func (a *app) VFS() *vfs.VFS                      { return a.fs }
func (a *app) SecurityManager() *security.Manager { return a.sec }

func createRouter(filename string) (*Router, error) {
	fs := new(vfs.VFS)
	fs.AddMount("/app/config", testdataBaseDir())

	appCfg, _ := config.ParseString(`routes {
			localhost {
				host = "localhost"
				port = "8080"
			}
		}`)

	l, _ := log.New(config.NewEmpty())
	l.SetWriter(ioutil.Discard)

	sec := security.New()
	sec.AddAuthScheme("form_auth", &scheme.FormAuth{LoginSubmitURL: "/login"})
	sec.AddAuthScheme("form", &scheme.FormAuth{LoginSubmitURL: "/login"})

	// config path in vfs, filepath.Join not required
	return NewWithApp(&app{cfg: appCfg, l: l, fs: fs, sec: sec}, "/app/config/"+filename)
}

func createHTTPRequest(host, path string) *http.Request {
	req := &http.Request{
		Host: host,
	}

	if !ess.IsStrEmpty(path) {
		req.URL = &url.URL{Path: path}
	}

	return req
}

func testdataBaseDir() string {
	wd, _ := os.Getwd()
	if idx := strings.Index(wd, "testdata"); idx > 0 {
		wd = wd[:idx]
	}
	return filepath.Join(wd, "testdata")
}
