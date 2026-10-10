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
type Router struct {
	mux    *http.ServeMux
	prefix string
	mws    []Middleware
	top    *[]Middleware // nil below the root
	once   sync.Once
	h      http.Handler
}

// NewRouter returns an empty Router.
func NewRouter() *Router {
	return &Router{mux: http.NewServeMux(), top: new([]Middleware)}
}

// Use adds middleware. On the root it runs for every request, before routing
// and for unmatched paths too. In a Group or Route it wraps that group's routes.
func (rt *Router) Use(mws ...Middleware) {
	if rt.top != nil {
		*rt.top = append(*rt.top, mws...)
		return
	}
	rt.mws = append(rt.mws, mws...)
}

func (rt *Router) child(prefix string, mws ...Middleware) *Router {
	return &Router{mux: rt.mux, prefix: rt.prefix + prefix, mws: append(slices.Clone(rt.mws), mws...)}
}

// Group registers routes that share middleware added inside fn.
func (rt *Router) Group(fn func(r *Router)) { fn(rt.child("")) }

// Route registers routes under prefix.
func (rt *Router) Route(prefix string, fn func(r *Router)) { fn(rt.child(prefix)) }

// With returns a Router whose routes also run mws.
func (rt *Router) With(mws ...Middleware) *Router { return rt.child("", mws...) }

// Mount serves prefix and everything below it with h. h sees the full path.
func (rt *Router) Mount(prefix string, h http.Handler) {
	full := rt.prefix + prefix
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

// ServeHTTP runs the root middleware and then the matching route.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.once.Do(func() {
		var h http.Handler = rt.mux
		if rt.top != nil {
			for i := len(*rt.top) - 1; i >= 0; i-- {
				h = (*rt.top)[i](h)
			}
		}
		rt.h = h
	})
	rt.h.ServeHTTP(w, r)
}

type routePatternKey struct{}

// TrackRoutePattern returns a request whose Router route, once served, is
// readable from the returned pointer, such as "/api/services/{addr}".
func TrackRoutePattern(r *http.Request) (*http.Request, *string) {
	p := new(string)
	return r.WithContext(context.WithValue(r.Context(), routePatternKey{}, p)), p
}
