package toxiproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/Shopify/toxiproxy/v2/toxics"
)

func stopBrowsersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.UserAgent(), "Mozilla/") {
			http.Error(w, "User agent not allowed", 403)
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

func timeoutMiddleware(next http.Handler) http.Handler {
	return http.TimeoutHandler(next, 25*time.Second, "")
}

type ApiServer struct {
	Collection *ProxyCollection
	Metrics    *metricsContainer
	Logger     *zerolog.Logger
	http       *http.Server
}

const (
	wait_timeout = 30 * time.Second
	read_timeout = 15 * time.Second
)

func NewServer(m *metricsContainer, logger zerolog.Logger) *ApiServer {
	return &ApiServer{
		Collection: NewProxyCollection(),
		Metrics:    m,
		Logger:     &logger,
	}
}

func (server *ApiServer) Listen(addr string) error {
	server.Logger.
		Info().
		Str("address", addr).
		Msg("Starting Toxiproxy HTTP server")

	server.http = &http.Server{
		Addr:         addr,
		Handler:      server.Routes(),
		WriteTimeout: wait_timeout,
		ReadTimeout:  read_timeout,
		IdleTimeout:  60 * time.Second,
	}

	err := server.http.ListenAndServe()
	if err == http.ErrServerClosed {
		err = nil
	}

	return err
}

func (server *ApiServer) Shutdown() error {
	if server.http == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), wait_timeout)
	defer cancel()

	err := server.http.Shutdown(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (server *ApiServer) Routes() *mux.Router {
	r := mux.NewRouter()
	r.Use(hlog.NewHandler(*server.Logger))
	r.Use(hlog.RequestIDHandler("request_id", "X-Toxiproxy-Request-Id"))
	r.Use(hlog.AccessHandler(func(r *http.Request, status, size int, duration time.Duration) {
		handler := mux.CurrentRoute(r).GetName()
		zerolog.Ctx(r.Context()).
			Debug().
			Str("client", r.RemoteAddr).
			Str("method", r.Method).
			Stringer("url", r.URL).
			Str("user_agent", r.Header.Get("User-Agent")).
			Int("status", status).
			Int("size", size).
			Dur("duration", duration).
			Str("handler", handler).
			Msg("")
	}))
	r.Use(stopBrowsersMiddleware)
	r.Use(timeoutMiddleware)

	r.HandleFunc("/reset", server.ResetState).Methods("POST").
		Name("ResetState")
	r.HandleFunc("/proxies", server.ProxyIndex).Methods("GET").
		Name("ProxyIndex")
	r.HandleFunc("/proxies", server.ProxyCreate).Methods("POST").
		Name("ProxyCreate")
	r.HandleFunc("/populate", server.Populate).Methods("POST").
		Name("Populate")
	r.HandleFunc("/proxies/{proxy}", server.ProxyShow).Methods("GET").
		Name("ProxyShow")
	r.HandleFunc("/proxies/{proxy}", server.ProxyUpdate).Methods("POST", "PATCH").
		Name("ProxyUpdate")
	r.HandleFunc("/proxies/{proxy}", server.ProxyDelete).Methods("DELETE").
		Name("ProxyDelete")
	r.HandleFunc("/proxies/{proxy}/toxics", server.ToxicIndex).Methods("GET").
		Name("ToxicIndex")
	r.HandleFunc("/proxies/{proxy}/toxics", server.ToxicCreateWrapper).Methods("POST").
		Name("ToxicCreate")
	r.HandleFunc("/proxies/{proxy}/toxics/{toxic}", server.ToxicShowWrapper).Methods("GET").
		Name("ToxicShow")
	r.HandleFunc("/proxies/{proxy}/toxics/{toxic}", server.ToxicUpdateWrapper).Methods("POST", "PATCH").
		Name("ToxicUpdate")
	r.HandleFunc("/proxies/{proxy}/toxics/{toxic}", server.ToxicDelete).Methods("DELETE").
		Name("ToxicDelete")

	r.HandleFunc("/version", server.Version).Methods("GET").Name("Version")

	if server.Metrics.anyMetricsEnabled() {
		r.Handle("/metrics", server.Metrics.handler()).Name("Metrics")
	}

	return r
}

func (server *ApiServer) PopulateConfig(data io.Reader) {
	logger := server.Logger
	proxies, err := server.Collection.PopulateJson(server, data)
	if err != nil {
		fmt.Println(err)
		logger.Err(err).Msg("Failed to populate proxies from file")
	} else {
		logger.Info().Int("proxies", len(proxies)).Msg("Populated proxies from file")
	}
}

func (server *ApiServer) ProxyIndex(response http.ResponseWriter, request *http.Request) {
	proxies := server.Collection.Proxies()
	marshalData := make(map[string]interface{}, len(proxies))

	for name, proxy := range proxies {
		marshalData[name] = proxyWithToxics(proxy)
	}

	data, err := json.Marshal(marshalData)
	if server.apiError(response, err) {
		return
	}

	response.Header().Set("Content-Type", "application/json")
	_, err = response.Write(data)
	if err != nil {
		log := zerolog.Ctx(request.Context())
		log.Warn().Err(err).Msg("ProxyIndex: Failed to write response to client")
	}
}

func (server *ApiServer) ResetState(response http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	proxies := server.Collection.Proxies()

	for _, proxy := range proxies {
		err := proxy.Start()
		if err != ErrProxyAlreadyStarted && server.apiError(response, err) {
			return
		}

		proxy.Toxics.ResetToxics(ctx)
	}

	response.WriteHeader(http.StatusNoContent)
	_, err := response.Write(nil)
	if err != nil {
		log := zerolog.Ctx(ctx)
		log.Warn().Err(err).Msg("ResetState: Failed to write headers to client")
	}
}

func (server *ApiServer) ProxyCreate(response http.ResponseWriter, request *http.Request) {
	// Default fields to enable the proxy right away
	input := Proxy{Enabled: true}
	err := json.NewDecoder(request.Body).Decode(&input)
	if server.apiError(response, joinError(err, ErrBadRequestBody)) {
		return
	}

	if len(input.Name) < 1 {
		server.apiError(response, joinError(fmt.Errorf("name"), ErrMissingField))
		return
	}
	if len(input.Upstream) < 1 {
		server.apiError(response, joinError(fmt.Errorf("upstream"), ErrMissingField))
		return
	}

	proxy := NewProxy(server, input.Name, input.Listen, input.Upstream)

	err = server.Collection.Add(proxy, input.Enabled)
	if server.apiError(response, err) {
		return
	}

	data, err := json.Marshal(proxyWithToxics(proxy))
	if server.apiError(response, err) {
		return
	}

	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusCreated)
	_, err = response.Write(data)
	if err != nil {
		log := zerolog.Ctx(request.Context())
		log.Warn().Err(err).Msg("ProxyCreate: Failed to write response to client")
	}
}

func (server *ApiServer) Populate(response http.ResponseWriter, request *http.Request) {
	proxies, err := server.Collection.PopulateJson(server, request.Body)
	log := zerolog.Ctx(request.Context())
	if err != nil {
		log.Warn().Err(err).Msg("Populate errors")
	}

	apiErr, ok := err.(*ApiError)
	if !ok && err != nil {
		log.Warn().Err(err).Msg("Error did not include status code")
		apiErr = &ApiError{err.Error(), http.StatusInternalServerError}
	}

	data, err := json.Marshal(struct {
		*ApiError `json:",omitempty"`
		Proxies   []proxyToxics `json:"proxies"`
	}{apiErr, proxiesWithToxics(proxies)})
	if server.apiError(response, err) {
		return
	}

	responseCode := http.StatusCreated
	if apiErr != nil {
		responseCode = apiErr.StatusCode
	}

	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(responseCode)
	_, err = response.Write(data)
	if err != nil {
		log.Warn().Err(err).Msg("Populate: Failed to write response to client")
	}
}

func (server *ApiServer) ProxyShow(response http.ResponseWriter, request *http.Request) {
	vars := mux.Vars(request)

	proxy, err := server.Collection.Get(vars["proxy"])
	if server.apiError(response, err) {
		return
	}

	data, err := json.Marshal(proxyWithToxics(proxy))
	if server.apiError(response, err) {
		return
	}

	response.Header().Set("Content-Type", "application/json")
	_, err = response.Write(data)
	if err != nil {
		server.Logger.Warn().Err(err).Msg("ProxyShow: Failed to write response to client")
	}
}

func (server *ApiServer) ProxyUpdate(response http.ResponseWriter, request *http.Request) {
	log := zerolog.Ctx(request.Context())
	if request.Method == "POST" {
		log.Warn().Msg("ProxyUpdate: HTTP method POST is depercated. Use HTTP PATCH instead.")
	}

	vars := mux.Vars(request)

	proxy, err := server.Collection.Get(vars["proxy"])
	if server.apiError(response, err) {
		return
	}

	// Default fields are the same as existing proxy
	input := Proxy{Listen: proxy.Listen, Upstream: proxy.Upstream, Enabled: proxy.Enabled}
	err = json.NewDecoder(request.Body).Decode(&input)
	if server.apiError(response, joinError(err, ErrBadRequestBody)) {
		return
	}

	err = proxy.Update(&input)
	if server.apiError(response, err) {
		return
	}

	data, err := json.Marshal(proxyWithToxics(proxy))
	if server.apiError(response, err) {
		return
	}

	response.Header().Set("Content-Type", "application/json")
	_, err = response.Write(data)
	if err != nil {
		log.Warn().Err(err).Msg("ProxyUpdate: Failed to write response to client")
	}
}

func (server *ApiServer) ProxyDelete(response http.ResponseWriter, request *http.Request) {
	vars := mux.Vars(request)

	err := server.Collection.Remove(vars["proxy"])
	if server.apiError(response, err) {
		return
	}

	response.WriteHeader(http.StatusNoContent)
	_, err = response.Write(nil)
	if err != nil {
		log := zerolog.Ctx(request.Context())
		log.Warn().Err(err).Msg("ProxyDelete: Failed to write headers to client")
	}
}

func (server *ApiServer) ToxicIndex(response http.ResponseWriter, request *http.Request) {
	vars := mux.Vars(request)

	proxy, err := server.Collection.Get(vars["proxy"])
	if server.apiError(response, err) {
		return
	}

	toxics := proxy.Toxics.GetToxicArray()
	data, err := json.Marshal(toxics)
	if server.apiError(response, err) {
		return
	}

	response.Header().Set("Content-Type", "application/json")
	_, err = response.Write(data)
	if err != nil {
		log := zerolog.Ctx(request.Context())
		log.Warn().Err(err).Msg("ToxicIndex: Failed to write response to client")
	}
}

func (server *ApiServer) ToxicCreateWrapper(response http.ResponseWriter, request *http.Request) {
	server.ToxicCreate(response, request)
}
func (server *ApiServer) ToxicCreate(input ...interface{}) {
	switch v := input[1].(type) {
	case *http.Request:
		res := input[0].(http.ResponseWriter)
		req := v
		vars := mux.Vars(req)
		proxy, err := server.Collection.Get(vars["proxy"])
		if server.apiError(res, err) {
			return
		}
		toxic, err := proxy.Toxics.AddToxicJson(v.Body)
		if server.apiError(res, err) {
			return
		}
		data, err := json.Marshal(toxic)
		if server.apiError(res, err) {
			return
		}
		res.Header().Set("Content-Type", "application/json")
		_, err = res.Write(data)
		if err != nil {
			log := zerolog.Ctx(req.Context())
			log.Warn().Err(err).Msg("ToxicCreate: Failed to write response to client")
		}
	case io.Reader:
		toxicdata := v
		passedproxy := input[0].(string)
		proxy, err := server.Collection.Get(passedproxy)
		if err != nil {
			println(err)
		}

		_, err = proxy.Toxics.AddToxicJson(toxicdata)
		if err != nil {
			println(err)
		}
	default:
		panic("type not supported")
	}

}

func (server *ApiServer) ToxicShowWrapper(response http.ResponseWriter, request *http.Request) {
	server.ToxicShow(response, request)
}
func (server *ApiServer) ToxicShow(input ...interface{}) interface{} {
	switch v := input[1].(type) {
	case *http.Request:
		res := input[0].(http.ResponseWriter)
		req := v
		vars := mux.Vars(req)
		proxy, err := server.Collection.Get(vars["proxy"])
		if server.apiError(res, err) {
			return false
		}
		toxic := proxy.Toxics.GetToxic(vars["toxic"])
		if toxic == nil {
			server.apiError(res, ErrToxicNotFound)
			return false
		}
		data, err := json.Marshal(toxic)
		if server.apiError(res, err) {
			return false
		}
		res.Header().Set("Content-Type", "application/json")
		_, err = res.Write(data)
		if err != nil {
			log := zerolog.Ctx(req.Context())
			log.Warn().Err(err).Msg("ToxicShow: Failed to write response to client")
			return false
		}
		return true
	case string:
		passedproxy := input[0].(string)
		passedtoxic := input[1].(string)
		proxy, err := server.Collection.Get(passedproxy)
		if err != nil {
			println(err)
		}

		toxic := proxy.Toxics.GetToxic(passedtoxic)
		if toxic != nil {
			return true
		}
		return false
	default:
		panic("type not supported")
	}
}
func (server *ApiServer) ToxicList(input ...interface{}) map[string]*toxics.ToxicWrapper {
	switch v := input[0].(type) {
	// case *http.Request: TODO
	case string:
		passedproxy := v
		toxicwrappermap := make(map[string]*toxics.ToxicWrapper)
		proxy, err := server.Collection.Get(passedproxy)
		if err != nil {
			println(err)
		}

		toxicarray := proxy.Toxics.GetToxicArray()
		if toxicarray == nil {
			return nil
		}
		for x := range toxicarray {
			tw := toxicarray[x].(*toxics.ToxicWrapper)
			toxicwrappermap[tw.Name] = tw

		}
		return toxicwrappermap
	default:
		panic("type not supported")
	}
}
func (server *ApiServer) ToxicDiff(proxyname string, configtoxics []FileToxics) map[string]string {
	// This only works for latency
	toxiclist := server.ToxicList(proxyname)
	toxicdiff := make(map[string]string)
	for _, ct := range configtoxics {
		flag := 0
		if _, ok := toxiclist[ct.Name]; ok {
			latency := toxiclist[ct.Name].Toxic.(*toxics.LatencyToxic)
			fmt.Printf("Da toxic %v\n", toxiclist[ct.Name].Type)
			// Im checking only latency and jitter this needs some better code
			if latency.Latency != int64(ct.Attributes["latency"]) {
				flag = 1
			}
			if latency.Jitter != int64(ct.Attributes["jitter"]) {
				flag = 1
			}
			if flag != 0 {
				toxicdiff[ct.Name]="update"
			}

		} else {
			toxicdiff[ct.Name]="add"
		}
	}
	return toxicdiff
}

func (server *ApiServer) ToxicUpdateWrapper(response http.ResponseWriter, request *http.Request) {
	server.ToxicUpdate(response, request)
}
func (server *ApiServer) ToxicUpdate(input ...interface{}) interface{} {
	switch v := input[1].(type) {
	case string:
		proxyname:= input[0].(string)
		toxic :=  input[1].(string)
		toxicdata := input[2].(io.Reader)
		proxy, err := server.Collection.Get(proxyname)
		if err != nil {
			println(err)
		}
		proxy.Toxics.UpdateToxicJson(toxic,toxicdata)
	case *http.Request:
		res := input[0].(http.ResponseWriter)
		req := v
		log := zerolog.Ctx(req.Context())
		if req.Method == "POST" {
			log.Warn().Msg("ToxicUpdate: HTTP method POST is depercated. Use HTTP PATCH instead.")
		}

		vars := mux.Vars(req)

		proxy, err := server.Collection.Get(vars["proxy"])
		if server.apiError(res, err) {
			return nil
		}

		toxic, err := proxy.Toxics.UpdateToxicJson(vars["toxic"], req.Body)
		if server.apiError(res, err) {
			return nil
		}

		data, err := json.Marshal(toxic)
		if server.apiError(res, err) {
			return nil
		}

		res.Header().Set("Content-Type", "application/json")
		_, err = res.Write(data)
		if err != nil {
			log.Warn().Err(err).Msg("ToxicUpdate: Failed to write response to client")
		}
	default:
		panic("here Type not supported")
	}
	return nil
}

func (server *ApiServer) ToxicDelete(response http.ResponseWriter, request *http.Request) {
	vars := mux.Vars(request)
	ctx := request.Context()
	log := zerolog.Ctx(ctx)

	proxy, err := server.Collection.Get(vars["proxy"])
	if server.apiError(response, err) {
		return
	}

	err = proxy.Toxics.RemoveToxic(ctx, vars["toxic"])
	if server.apiError(response, err) {
		return
	}

	response.WriteHeader(http.StatusNoContent)
	_, err = response.Write(nil)
	if err != nil {
		log.Warn().Err(err).Msg("ToxicDelete: Failed to write headers to client")
	}
}

func (server *ApiServer) Version(response http.ResponseWriter, request *http.Request) {
	log := zerolog.Ctx(request.Context())

	response.Header().Set("Content-Type", "application/json;charset=utf-8")
	version := fmt.Sprintf("{\"version\": \"%s\"}\n", Version)
	_, err := response.Write([]byte(version))
	if err != nil {
		log.Warn().Err(err).Msg("Version: Failed to write response to client")
	}
}

type ApiError struct {
	Message    string `json:"error"`
	StatusCode int    `json:"status"`
}

func (e *ApiError) Error() string {
	return e.Message
}

func newError(msg string, status int) *ApiError {
	return &ApiError{msg, status}
}

func joinError(err error, wrapper *ApiError) *ApiError {
	if err != nil {
		return &ApiError{wrapper.Message + ": " + err.Error(), wrapper.StatusCode}
	}
	return nil
}

var (
	ErrBadRequestBody     = newError("bad request body", http.StatusBadRequest)
	ErrMissingField       = newError("missing required field", http.StatusBadRequest)
	ErrProxyNotFound      = newError("proxy not found", http.StatusNotFound)
	ErrProxyAlreadyExists = newError("proxy already exists", http.StatusConflict)
	ErrInvalidStream      = newError(
		"stream was invalid, can be either upstream or downstream",
		http.StatusBadRequest,
	)
	ErrInvalidToxicType   = newError("invalid toxic type", http.StatusBadRequest)
	ErrToxicAlreadyExists = newError("toxic already exists", http.StatusConflict)
	ErrToxicNotFound      = newError("toxic not found", http.StatusNotFound)
)

func (server *ApiServer) apiError(resp http.ResponseWriter, err error) bool {
	obj, ok := err.(*ApiError)
	if !ok && err != nil {
		server.Logger.Warn().Err(err).Msg("Error did not include status code")
		obj = &ApiError{err.Error(), http.StatusInternalServerError}
	}

	if obj == nil {
		return false
	}

	data, err2 := json.Marshal(obj)
	if err2 != nil {
		server.Logger.Warn().Err(err2).Msg("Error json encoding error (╯°□°）╯︵ ┻━┻ ")
	}
	resp.Header().Set("Content-Type", "application/json")
	http.Error(resp, string(data), obj.StatusCode)

	return true
}

type proxyToxics struct {
	*Proxy
	Toxics []toxics.Toxic `json:"toxics"`
}

func proxyWithToxics(proxy *Proxy) (result proxyToxics) {
	result.Proxy = proxy
	result.Toxics = proxy.Toxics.GetToxicArray()
	return
}

func proxiesWithToxics(proxies []*Proxy) (result []proxyToxics) {
	for _, proxy := range proxies {
		result = append(result, proxyWithToxics(proxy))
	}
	return
}
