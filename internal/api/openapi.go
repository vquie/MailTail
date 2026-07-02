package api

import (
	"embed"
	"encoding/json"
	"net/http"
	"sort"
)

//go:embed openapi/openapi.json openapi/docs.html
var openAPIFiles embed.FS

type documentedOperation struct {
	Method string
	Path   string
}

var documentedAPIOperations = []documentedOperation{
	{Method: http.MethodGet, Path: "/api/app"},
	{Method: http.MethodGet, Path: "/api/session"},
	{Method: http.MethodGet, Path: "/api/settings"},
	{Method: http.MethodPut, Path: "/api/settings"},
	{Method: http.MethodGet, Path: "/api/admin/mailbox-settings"},
	{Method: http.MethodPut, Path: "/api/admin/mailbox-settings"},
	{Method: http.MethodGet, Path: "/api/admin/users"},
	{Method: http.MethodPost, Path: "/api/admin/users"},
	{Method: http.MethodPut, Path: "/api/admin/users/{id}"},
	{Method: http.MethodDelete, Path: "/api/admin/users/{id}"},
	{Method: http.MethodGet, Path: "/api/messages"},
	{Method: http.MethodDelete, Path: "/api/messages"},
	{Method: http.MethodGet, Path: "/api/messages/{id}"},
	{Method: http.MethodDelete, Path: "/api/messages/{id}"},
	{Method: http.MethodGet, Path: "/api/messages/{id}/raw"},
	{Method: http.MethodGet, Path: "/api/messages/{id}/attachments/{attachmentId}"},
	{Method: http.MethodGet, Path: "/api/stats"},
	{Method: http.MethodPost, Path: "/auth/login"},
	{Method: http.MethodPost, Path: "/auth/logout"},
}

func loadOpenAPISpec() (map[string]any, error) {
	payload, err := openAPIFiles.ReadFile("openapi/openapi.json")
	if err != nil {
		return nil, err
	}

	var spec map[string]any
	if err := json.Unmarshal(payload, &spec); err != nil {
		return nil, err
	}
	return spec, nil
}

func readOpenAPISpecBytes() ([]byte, error) {
	return openAPIFiles.ReadFile("openapi/openapi.json")
}

func readAPIDocsHTML() ([]byte, error) {
	return openAPIFiles.ReadFile("openapi/docs.html")
}

func normalizedDocumentedOperations() []documentedOperation {
	operations := append([]documentedOperation(nil), documentedAPIOperations...)
	sort.Slice(operations, func(i, j int) bool {
		if operations[i].Path == operations[j].Path {
			return operations[i].Method < operations[j].Method
		}
		return operations[i].Path < operations[j].Path
	})
	return operations
}

func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	payload, err := readOpenAPISpecBytes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load OpenAPI spec")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func (s *Server) handleAPIDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if r.URL.Path != "/api/docs" && r.URL.Path != "/api/docs/" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	payload, err := readAPIDocsHTML()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load API docs")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}
