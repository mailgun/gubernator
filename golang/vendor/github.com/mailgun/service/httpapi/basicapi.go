package httpapi

import (
	"net/http"

	"github.com/gorilla/mux"
)

func InitDefaultAPI(router *mux.Router) {
	router.HandleFunc("/_ping", handlePing).Methods("GET")
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("pong"))
}
