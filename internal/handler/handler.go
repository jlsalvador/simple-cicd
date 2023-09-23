/*
Copyright 2023 José Luis Salvador Rufo <salvador.joseluis@gmail.com>.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package handler

import (
	"net/http"
	"strings"
)

type customHandler struct {
	urls map[string]func(w http.ResponseWriter, r *http.Request)
}

func (h customHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	p = strings.TrimRight(p, "/")

	if fn, ok := h.urls[p]; ok {
		fn(w, r)
	}
}

func (h *customHandler) RegisterHandler(path string, fn func(w http.ResponseWriter, r *http.Request)) {
	path = strings.Trim(path, "/")
	path = "/" + path

	h.urls[path] = fn
}

func (h *customHandler) UnregisterHandler(path string) {
	delete(h.urls, path)
}

func NewHandler() *customHandler {
	return &customHandler{
		urls: make(map[string]func(w http.ResponseWriter, r *http.Request)),
	}
}
