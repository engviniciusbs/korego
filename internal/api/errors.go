// Package api contém o roteador HTTP e utilitários de resposta da API.
package api

import (
	"encoding/json"
	"net/http"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteJSON escreve v como JSON com o status informado.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError escreve o erro no formato padrão {"error":{"code","message"}}.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// writeFieldError escreve um erro de validação com o campo afetado:
// {"error":{"code","message","field"}}.
func writeFieldError(w http.ResponseWriter, status int, code, message, field string) {
	WriteJSON(w, status, fieldErrorBody{Error: fieldErrorDetail{Code: code, Message: message, Field: field}})
}

type fieldErrorBody struct {
	Error fieldErrorDetail `json:"error"`
}

type fieldErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
}
