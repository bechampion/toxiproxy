package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
)

type server struct {
	collection *ProxyCollection
}

func NewServer(collection *ProxyCollection) *server {
	return &server{
		collection: collection,
	}
}

func (server *server) Listen() {
	r := mux.NewRouter()
	r.HandleFunc("/proxies", server.ProxyIndex).Methods("GET")
	r.HandleFunc("/proxies", server.ProxyCreate).Methods("POST")
	r.HandleFunc("/proxies/{proxy}", server.ProxyDelete).Methods("DELETE")
	r.HandleFunc("/proxies/{proxy}/upstream/toxics", server.ToxicIndexUpstream).Methods("GET")
	r.HandleFunc("/proxies/{proxy}/downstream/toxics", server.ToxicIndexDownstream).Methods("GET")
	r.HandleFunc("/proxies/{proxy}/upstream/toxics/{toxic}", server.ToxicSetUpstream).Methods("POST")
	r.HandleFunc("/proxies/{proxy}/downstream/toxics/{toxic}", server.ToxicSetDownstream).Methods("POST")

	r.HandleFunc("/version", server.Version).Methods("GET")
	http.Handle("/", r)

	logrus.WithFields(logrus.Fields{
		"host": apiHost,
		"port": apiPort,
	}).Info("API HTTP server starting")

	err := http.ListenAndServe(net.JoinHostPort(apiHost, apiPort), nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func (server *server) ProxyIndex(response http.ResponseWriter, request *http.Request) {
	data, err := json.Marshal(server.collection.Proxies())
	if err != nil {
		http.Error(response, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}

	response.Header().Set("Content-Type", "application/json")
	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ProxyIndex: Failed to write response to client", err)
	}
}

func (server *server) ProxyCreate(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	proxy := NewProxy()
	err := json.NewDecoder(request.Body).Decode(&proxy)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	err = proxy.Start()
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusConflict), http.StatusConflict)
		return
	}

	err = server.collection.Add(proxy)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusConflict), http.StatusConflict)
		return
	}

	data, err := json.Marshal(&proxy)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	response.WriteHeader(http.StatusCreated)
	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ProxyIndex: Failed to write response to client", err)
	}
}

func (server *server) ProxyDelete(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	err := server.collection.Remove(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	response.WriteHeader(http.StatusNoContent)
	_, err = response.Write(nil)
	if err != nil {
		logrus.Warn("ProxyIndex: Failed to write headers to client", err)
	}
}

func (server *server) ToxicIndexUpstream(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	proxy, err := server.collection.Get(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	data, err := json.Marshal(proxy.upToxics)
	if err != nil {
		http.Error(response, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}

	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ToxicIndex: Failed to write response to client", err)
	}
}

func (server *server) ToxicIndexDownstream(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	proxy, err := server.collection.Get(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	data, err := json.Marshal(proxy.downToxics)
	if err != nil {
		http.Error(response, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}

	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ToxicIndex: Failed to write response to client", err)
	}
}

func (server *server) ToxicSetUpstream(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	proxy, err := server.collection.Get(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	toxic, err := NewToxicFromJson(vars["toxic"], request.Body)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	err = proxy.upToxics.SetToxic(toxic)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	data, err := json.Marshal(toxic)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ToxicSet: Failed to write response to client", err)
	}
}

func (server *server) ToxicSetDownstream(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	proxy, err := server.collection.Get(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	toxic, err := NewToxicFromJson(vars["toxic"], request.Body)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	err = proxy.downToxics.SetToxic(toxic)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	data, err := json.Marshal(toxic)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ToxicSet: Failed to write response to client", err)
	}
}

func (server *server) Version(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "text/plain")
	_, err := response.Write([]byte(Version))
	if err != nil {
		logrus.Warn("Version: Failed to write response to client", err)
	}
}

func (server *server) apiError(err error, code int) string {
	data, err2 := json.Marshal(struct {
		Title  string `json:"title"`
		Status int    `json:"status"`
	}{err.Error(), code})
	if err2 != nil {
		logrus.Warn("Error json encoding error (╯°□°）╯︵ ┻━┻", err2)
		return ""
	}
	return string(data)
}
