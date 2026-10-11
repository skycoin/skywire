// Package httputil pkg/httputil/router.go
package httputil

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
)

// Middleware wraps a handler.
type Middleware = func(http.Handler) http.Handler

// Router is an http.ServeMux with middleware groups and path prefixes. Paths
// use ServeMux wildcards ({pk}, {rest...}). A path ending in "/" matches only
// itself, and Get("/") inside Route("/api") answers both "/api" and "/api/".
// Route owns its prefix: an unmatched path below it gets 404 or 405 there and
// never reaches a broader route such as "/{rest...}".
type Router struct {
	mux    *http.ServeMux
	prefix string
	mws    []Middleware
	shared *routerShared // set on the root only
	root   *Router
}

type routerShared struct {
	top    []Middleware
	owned  map[string]bool // Route prefixes with an unmatched handler
	exact  map[string]bool // paths registered for any method
	once   sync.Once
	served http.Handler
}

// NewRouter returns an empty Router.
func NewRouter() *Router {
	rt := &Router{mux: http.NewServeMux(), shared: &routerShared{owned: map[string]bool{}, exact: map[string]bool{}}}
	rt.root = rt
	return rt
}

// Use adds middleware. On the root it runs for every request, before routing
// and for unmatched paths too. In a Group or Route it wraps that group's routes.
func (rt *Router) Use(mws ...Middleware) {
	if rt.shared != nil {
		rt.shared.top = append(rt.shared.top, mws...)
		return
	}
	rt.mws = append(rt.mws, mws...)
}

func (rt *Router) child(prefix string, mws ...Middleware) *Router {
	return &Router{mux: rt.mux, root: rt.root, prefix: strings.TrimSuffix(rt.prefix+prefix, "/"), mws: append(slices.Clone(rt.mws), mws...)}
}

// Group registers routes that share middleware added inside fn.
func (rt *Router) Group(fn func(r *Router)) { fn(rt.child("")) }

// Route registers routes under prefix.
func (rt *Router) Route(prefix string, fn func(r *Router)) {
	c := rt.child(prefix)
	fn(c)
	if c.prefix != "" && !rt.root.shared.owned[c.prefix] {
		rt.root.shared.owned[c.prefix] = true
		rt.mux.Handle(c.prefix+"/", c.wrap(c.prefix+"/*", http.HandlerFunc(rt.root.unmatched)))
		if !rt.root.shared.exact[c.prefix] {
			// Without this ServeMux would redirect the bare prefix to prefix/.
			rt.mux.Handle(c.prefix, c.wrap(c.prefix, http.HandlerFunc(rt.root.unmatched)))
		}
	}
}

// With returns a Router whose routes also run mws.
func (rt *Router) With(mws ...Middleware) *Router { return rt.child("", mws...) }

// Mount serves prefix and everything below it with h. h sees the full path.
func (rt *Router) Mount(prefix string, h http.Handler) {
	full := strings.TrimSuffix(rt.prefix+prefix, "/")
	wrapped := rt.wrap(full+"/*", h)
	rt.mux.Handle(full, wrapped)
	rt.mux.Handle(full+"/", wrapped)
}

// Handle registers h for path and any method.
func (rt *Router) Handle(path string, h http.Handler) { rt.handle("", path, h) }

// HandleFunc registers h for path and any method.
func (rt *Router) HandleFunc(path string, h http.HandlerFunc) { rt.handle("", path, h) }

// Get registers h for GET (and HEAD) on path.
func (rt *Router) Get(path string, h http.HandlerFunc) { rt.handle(http.MethodGet, path, h) }

// Post registers h for POST on path.
func (rt *Router) Post(path string, h http.HandlerFunc) { rt.handle(http.MethodPost, path, h) }

// Put registers h for PUT on path.
func (rt *Router) Put(path string, h http.HandlerFunc) { rt.handle(http.MethodPut, path, h) }

// Patch registers h for PATCH on path.
func (rt *Router) Patch(path string, h http.HandlerFunc) { rt.handle(http.MethodPatch, path, h) }

// Delete registers h for DELETE on path.
func (rt *Router) Delete(path string, h http.HandlerFunc) { rt.handle(http.MethodDelete, path, h) }

func (rt *Router) handle(method, path string, h http.Handler) {
	if strings.Contains(path, "*") {
		panic("httputil.Router: use {name...} rather than * in " + path)
	}
	full := rt.prefix + path
	wrapped := rt.wrap(full, h)
	if path == "/" && rt.prefix != "" {
		rt.register(method, rt.prefix, wrapped)
	}
	if strings.HasSuffix(full, "/") {
		full += "{$}"
	}
	rt.register(method, full, wrapped)
}

func (rt *Router) register(method, path string, h http.Handler) {
	if method == "" {
		rt.root.shared.exact[path] = true
	}
	if method != "" {
		path = method + " " + path
	}
	rt.mux.Handle(path, h)
}

// wrap applies the group middleware and records the route for RoutePattern.
func (rt *Router) wrap(pattern string, h http.Handler) http.Handler {
	for i := len(rt.mws) - 1; i >= 0; i-- {
		h = rt.mws[i](h)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := r.Context().Value(routePatternKey{}).(*string); ok {
			*p = pattern
		}
		h.ServeHTTP(w, r)
	})
}

var routeMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// unmatched answers a path below a Route prefix that no route took: 405 with
// Allow when the path exists for other methods, else 404.
func (rt *Router) unmatched(w http.ResponseWriter, r *http.Request) {
	own := r.Pattern
	var allow []string
	for _, m := range routeMethods {
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, p := rt.mux.Handler(probe); p != "" && p != own {
			allow = append(allow, m)
		}
	}
	if len(allow) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Allow", strings.Join(allow, ", "))
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
}

// ServeHTTP runs the root middleware and then the matching route.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s := rt.root.shared
	s.once.Do(func() {
		var h http.Handler = rt.mux
		for i := len(s.top) - 1; i >= 0; i-- {
			h = s.top[i](h)
		}
		s.served = h
	})
	s.served.ServeHTTP(w, r)
}

type routePatternKey struct{}

// TrackRoutePattern returns a request whose Router route, once served, is
// readable from the returned pointer, such as "/api/services/{addr}".
func TrackRoutePattern(r *http.Request) (*http.Request, *string) {
	p := new(string)
	return r.WithContext(context.WithValue(r.Context(), routePatternKey{}, p)), p
}
