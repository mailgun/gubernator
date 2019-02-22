package debug

import (
	"net/http"
	"net/http/pprof"

	"github.com/gorilla/mux"
	"golang.org/x/net/trace"
)

func InitPprofAPI(router *mux.Router) {
	router.HandleFunc("/_debug/pprof/", http.HandlerFunc(pprof.Index)).Methods("GET")
	router.HandleFunc("/_debug/pprof/profile", http.HandlerFunc(pprof.Profile)).Methods("GET")
	router.HandleFunc("/_debug/pprof/symbol", http.HandlerFunc(pprof.Symbol)).Methods("GET")
	router.HandleFunc("/_debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline)).Methods("GET")
	router.HandleFunc("/_debug/pprof/trace ", http.HandlerFunc(pprof.Trace)).Methods("GET")
	router.Handle("/_debug/pprof/heap", pprof.Handler("heap")).Methods("GET")
	router.Handle("/_debug/pprof/goroutine", pprof.Handler("goroutine")).Methods("GET")
	router.Handle("/_debug/pprof/threadcreate", pprof.Handler("threadcreate")).Methods("GET")
	router.Handle("/_debug/pprof/block", pprof.Handler("block")).Methods("GET")
	router.Handle("/_debug/pprof/mutex", pprof.Handler("mutex")).Methods("GET")
}

func InitHTTPTraceAPI(router *mux.Router) {
	router.HandleFunc("/_debug/requests", handleTraceRequests).Methods("GET")
	router.HandleFunc("/_debug/events", handleTraceEvents).Methods("GET")
}

func handleTraceRequests(w http.ResponseWriter, r *http.Request) {
	trace.Render(w, r, true)
}

func handleTraceEvents(w http.ResponseWriter, r *http.Request) {
	trace.RenderEvents(w, r, true)
}
