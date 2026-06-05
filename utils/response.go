package utils

import (
	"encoding/json"
	"net/http"
)

type envelope struct {
	Data  any    `json:"data"`
	Error *string `json:"error"`
}

func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(envelope{Data: data, Error: nil})
}

func Err(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(envelope{Data: nil, Error: &msg})
}
