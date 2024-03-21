package toxiproxy

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// ProxyCollection is a collection of proxies. It's the interface for anything
// to add and remove proxies from the toxiproxy instance. It's responsibility is
// to maintain the integrity of the proxy set, by guarding for things such as
// duplicate names.
type ProxyCollection struct {
	sync.RWMutex

	proxies map[string]*Proxy
}

func NewProxyCollection() *ProxyCollection {
	return &ProxyCollection{
		proxies: make(map[string]*Proxy),
	}
}

func (collection *ProxyCollection) Add(proxy *Proxy, start bool) error {
	collection.Lock()
	defer collection.Unlock()

	if _, exists := collection.proxies[proxy.Name]; exists {
		return ErrProxyAlreadyExists
	}

	if start {
		err := proxy.Start()
		if err != nil {
			return err
		}
	}

	collection.proxies[proxy.Name] = proxy

	return nil
}

func (collection *ProxyCollection) AddOrReplace(proxy *Proxy, start bool) error {
	collection.Lock()
	defer collection.Unlock()

	if existing, exists := collection.proxies[proxy.Name]; exists {
		if existing.Listen == proxy.Listen && existing.Upstream == proxy.Upstream {
			return nil
		}
		existing.Stop()
	}

	if start {
		err := proxy.Start()
		if err != nil {
			return err
		}
	}

	collection.proxies[proxy.Name] = proxy

	return nil
}

func (collection *ProxyCollection) PopulateJson(
	server *ApiServer,
	data io.Reader,
) ([]*Proxy, error) {
	input := []struct {
		Proxy
		Enabled *bool `json:"enabled"` // Overrides Proxy field to make field nullable
	}{}

	json.NewDecoder(data).Decode(&input)

	// Check for valid input before creating any proxies
	t := true
	for i := range input {
		if len(input[i].Name) < 1 {
			return nil, joinError(fmt.Errorf("name at proxy %d", i+1), ErrMissingField)
		}
		if len(input[i].Upstream) < 1 {
			return nil, joinError(fmt.Errorf("upstream at proxy %d", i+1), ErrMissingField)
		}
		if input[i].Enabled == nil {
			input[i].Enabled = &t
		}
	}

	proxies := make([]*Proxy, 0, len(input))
	tempinputmap := map[string]bool{}
	//lol soz
	for _,pe:= range input {
		tempinputmap[pe.Name]=true
	}

	if (len(tempinputmap) != len(collection.proxies)){
		for _,v := range collection.proxies {
			if _,ok := tempinputmap[v.Name] ; !ok {
				delete(collection.proxies,v.Name)

			}

		}
	}
    var err error
	toxicsmap := make(map[string]FileToxics)
	for i := range input {
		proxy := NewProxy(server, input[i].Name, input[i].Listen, input[i].Upstream)
		//Here we need to check the proxy is different before we do this ,  as this can have some overhead
		err = collection.AddOrReplace(proxy, *input[i].Enabled)
		if err != nil {
			return proxies, err
		}
		for _,ft := range input[i].FileToxics {
			toxicsmap[ft.Name]=ft

		}
		for tdk,tdv := range server.ToxicDiff(input[i].Name,input[i].FileToxics){
			toxicData,err := json.Marshal(toxicsmap[tdk])
			toxicreader:= strings.NewReader(string(toxicData))
			if err != nil {
				panic(err)
			}
			if tdv == "add"{
				server.ToxicCreate(input[i].Name,toxicreader)
			}
			if tdv == "update"{
				server.ToxicUpdate(input[i].Name , tdk, toxicreader)
			}
		}

		proxies = append(proxies, proxy)
	}
	return proxies, err
}

func (collection *ProxyCollection) Proxies() map[string]*Proxy {
	collection.RLock()
	defer collection.RUnlock()

	// Copy the map since using the existing one isn't thread-safe
	proxies := make(map[string]*Proxy, len(collection.proxies))
	for k, v := range collection.proxies {
		proxies[k] = v
	}
	return proxies
}

func (collection *ProxyCollection) Get(name string) (*Proxy, error) {
	collection.RLock()
	defer collection.RUnlock()

	return collection.getByName(name)
}

func (collection *ProxyCollection) Remove(name string) error {
	collection.Lock()
	defer collection.Unlock()

	proxy, err := collection.getByName(name)
	if err != nil {
		return err
	}
	proxy.Stop()

	delete(collection.proxies, proxy.Name)
	return nil
}

func (collection *ProxyCollection) Clear() error {
	collection.Lock()
	defer collection.Unlock()

	for _, proxy := range collection.proxies {
		proxy.Stop()

		delete(collection.proxies, proxy.Name)
	}

	return nil
}

// getByName returns a proxy by its name. Its used from #remove and #get.
// It assumes the lock has already been acquired.
func (collection *ProxyCollection) getByName(name string) (*Proxy, error) {
	proxy, exists := collection.proxies[name]
	if !exists {
		return nil, ErrProxyNotFound
	}
	return proxy, nil
}
