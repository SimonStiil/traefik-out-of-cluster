package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"
	traefikconfig "github.com/traefik/traefik/v3/pkg/config/dynamic"
	"k8s.io/client-go/util/homedir"
)

var (
	Config           ConfigType
	Kubeconfig       string
	childControllers []ChildController
	requests         = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_endpoint_requests_count",
		Help: "The amount of requests to an endpoint",
	}, []string{"endpoint", "method"},
	)
	exported_ingress_count = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "exported_ingress_count",
		Help: "Amount of exported ingresses found in cluster"})
	routes_created_count = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "routes_created_count",
		Help: "Amount of routes created in the config"})
	broken_ingress_count = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "broken_ingress_count",
		Help: "Amount of exported ingresses found in cluster that does not have a loadbalancer ip"})
	child_fetch_errors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "child_controller_fetch_errors_total",
		Help: "Total number of errors fetching from child controllers",
	}, []string{"child_name"},
	)
	child_fetch_success = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "child_controller_fetch_success_total",
		Help: "Total number of successful fetches from child controllers",
	}, []string{"child_name"},
	)

	client KubeClient
)

type Health struct {
	Status string `json:"status"`
}

func HealthActuator(w http.ResponseWriter, r *http.Request) {
	if Config.Prometheus.Enabled {
		requests.WithLabelValues(r.URL.EscapedPath(), r.Method).Inc()
	}
	if !(r.URL.Path == Config.Health.Endpoint) {
		log.Printf("@I %v %v %v %v - HealthActuator\n", r.Method, r.URL.Path, r.RemoteAddr, 404)
		http.NotFoundHandler().ServeHTTP(w, r)
		return
	}
	reply := Health{Status: "UP"}
	if Config.Debug || Config.Print.Ok {
		log.Printf("@I %v %v %v %v - HealthActuator\n", r.Method, r.URL.Path, r.RemoteAddr, 200)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
	return
}

func MainHandler(w http.ResponseWriter, r *http.Request) {
	if Config.Prometheus.Enabled {
		requests.WithLabelValues(r.URL.EscapedPath(), r.Method).Inc()
	}

	var finalConfig *traefikconfig.Configuration
	var err error

	// Check if we have child controllers configured
	if len(childControllers) > 0 {
		// Get local configuration
		localConfig, err := client.GetTraefikConfiguration()
		if err != nil {
			log.Printf("@W Error getting local configuration: %v\n", err)
			localConfig = nil // Continue without local config
		}

		// Get aggregated configuration from all sources
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		finalConfig, err = GetAggregatedConfiguration(ctx, childControllers, localConfig)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 Internal Server Error"))
			log.Printf("@E %v %v %v %v - Main Handler Aggregation Error - %+v\n", r.Method, r.URL.Path, r.RemoteAddr, 500, err.Error())
			return
		}
	} else {
		// No child controllers, just use local configuration
		finalConfig, err = client.GetTraefikConfiguration()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 Internal Server Error"))
			log.Printf("@E %v %v %v %v - Main Handler Request Error - %+v\n", r.Method, r.URL.Path, r.RemoteAddr, 500, err.Error())
			return
		}
	}

	if Config.Debug || Config.Print.Ok {
		log.Printf("@I %v %v %v %v - Main Handler\n", r.Method, r.URL.Path, r.RemoteAddr, 200)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(finalConfig)
	return
}

type ConfigType struct {
	Debug      bool                    `mapstructure:"Debug"`
	Print      PrintDebug              `mapstructure:"Print"`
	Port       string                  `mapstructure:"Port"`
	Cluster    ClusterConfig           `mapstructure:"Cluster"`
	Traefik    TraefikConfig           `mapstructure:"Traefik"`
	Prometheus PrometheusConfig        `mapstructure:"Prometheus"`
	Health     HealthConfig            `mapstructure:"Health"`
	Children   []ChildControllerConfig `mapstructure:"Children"`
}
type PrintDebug struct {
	Ok bool `mapstructure:"Ok"`
}
type ClusterConfig struct {
	Ingress        IngressConfig `mapstructure:"Ingress"`
	Kubeconfig     string        `mapstructure:"Kubeconfig"`
	RootCAFilename string        `mapstructure:"RootCAFilename"`
}
type IngressConfig struct {
	Address   string           `mapstructure:"Address"`
	HTTP      PortConfig       `mapstructure:"HTTP"`
	HTTPS     PortConfig       `mapstructure:"HTTPS"`
	Alternate IngressConfigAlt `mapstructure:"Alt"`
}
type IngressConfigAlt struct {
	HTTP  PortConfig `mapstructure:"HTTP"`
	HTTPS PortConfig `mapstructure:"HTTPS"`
}
type PortConfig struct {
	Port     string `mapstructure:"Port"`
	Protocol string `mapstructure:"Protocol"`
}
type TraefikConfig struct {
	HTTP  HTTPConfig `mapstructure:"HTTP"`
	HTTPS HTTPConfig `mapstructure:"HTTPS"`
}
type HTTPConfig struct {
	Entrypoint EntrypointConfig `mapstructure:"Entrypoint"`
}
type EntrypointConfig struct {
	Name string `mapstructure:"Name"`
}
type PrometheusConfig struct {
	Enabled  bool   `mapstructure:"Enabled"`
	Endpoint string `mapstructure:"Endpoint"`
}
type HealthConfig struct {
	Endpoint string `mapstructure:"Endpoint"`
}
type ChildControllerConfig struct {
	Name       string `mapstructure:"Name"`
	URL        string `mapstructure:"URL"`
	Timeout    int    `mapstructure:"Timeout"`    // Timeout in seconds
	RootCAFile string `mapstructure:"RootCAFile"` // Path to CA certificate file
}

func main() {
	DynamicConfig := *viper.New()
	DynamicConfig.SetDefault("Debug", false)
	DynamicConfig.SetDefault("Print.Ok", false)
	DynamicConfig.SetDefault("Port", 8080)
	DynamicConfig.SetDefault("Cluster.Kubeconfig", "")
	DynamicConfig.SetDefault("Cluster.RootCAFilename", "/etc/traefik/root.crt")
	DynamicConfig.SetDefault("Cluster.Ingress.Address", "")
	DynamicConfig.SetDefault("Cluster.Ingress.HTTP.Port", 80)
	DynamicConfig.SetDefault("Cluster.Ingress.HTTP.Protocol", "http")
	DynamicConfig.SetDefault("Cluster.Ingress.HTTPS.Port", 443)
	DynamicConfig.SetDefault("Cluster.Ingress.HTTPS.Protocol", "https")
	DynamicConfig.SetDefault("Cluster.Ingress.Alt.HTTP.Port", "")
	DynamicConfig.SetDefault("Cluster.Ingress.Alt.HTTPS.Port", "")
	DynamicConfig.SetDefault("Traefik.HTTP.Entrypoint.Name", "web")
	DynamicConfig.SetDefault("Traefik.HTTPS.Entrypoint.Name", "websecure")
	DynamicConfig.SetDefault("Prometheus.Enabled", true)
	DynamicConfig.SetDefault("Prometheus.Endpoint", "/metrics")
	DynamicConfig.SetDefault("Health.Endpoint", "/health")
	DynamicConfig.AutomaticEnv()

	for _, key := range DynamicConfig.AllKeys() {
		DynamicConfig.BindEnv(key, "TOOC_"+strings.ToUpper(strings.ReplaceAll(key, ".", "_")))
	}
	DynamicConfig.Unmarshal(&Config)

	// Load child controller configurations from environment variables
	childConfigs := make(map[int]*ChildControllerConfig)
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "TOOC_CHILDREN_") {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := parts[0]
			value := parts[1]

			// Extract index and field from TOOC_CHILDREN_0_NAME format
			after := strings.TrimPrefix(key, "TOOC_CHILDREN_")
			tokens := strings.SplitN(after, "_", 2)
			if len(tokens) != 2 {
				continue
			}

			index, err := strconv.Atoi(tokens[0])
			if err != nil {
				continue
			}

			field := tokens[1]

			if childConfigs[index] == nil {
				childConfigs[index] = &ChildControllerConfig{}
			}

			switch field {
			case "NAME":
				childConfigs[index].Name = value
			case "URL":
				childConfigs[index].URL = value
			case "TIMEOUT":
				if timeout, err := strconv.Atoi(value); err == nil {
					childConfigs[index].Timeout = timeout
				}
			case "ROOTCAFILE":
				childConfigs[index].RootCAFile = value
			}
		}
	}

	// Add child configs to Config in order
	for i := 0; i < len(childConfigs); i++ {
		if childConfig, ok := childConfigs[i]; ok && childConfig.Name != "" && childConfig.URL != "" {
			Config.Children = append(Config.Children, *childConfig)
			if Config.Debug {
				log.Printf("@D Loaded child config %d: %+v\n", i, *childConfig)
			}
		}
	}

	if Config.Debug {
		log.Println("@D viper keys:")
		for _, key := range DynamicConfig.AllKeys() {
			if Config.Debug {
				log.Printf("@D -   %v => %v\n", key, "TOOC_"+strings.ToUpper(strings.ReplaceAll(key, ".", "_")))
			}
		}
	}
	if Config.Debug {
		log.Printf("@D Config %+v\n", Config)
	}

	Kubeconfig = Config.Cluster.Kubeconfig
	if Kubeconfig == "" {
		if home := homedir.HomeDir(); home != "" {
			homeConfig := filepath.Join(home, ".kube", "config")
			if _, err := os.Stat(homeConfig); err == nil {
				Kubeconfig = homeConfig
			}
		}
	}

	// Initialize child controllers
	for _, childConfig := range Config.Children {
		timeout := 10 * time.Second
		if childConfig.Timeout > 0 {
			timeout = time.Duration(childConfig.Timeout) * time.Second
		}
		childControllers = append(childControllers, ChildController{
			Name:       childConfig.Name,
			URL:        childConfig.URL,
			Timeout:    timeout,
			RootCAFile: childConfig.RootCAFile,
		})
		log.Printf("@I Registered child controller: %s (%s)\n", childConfig.Name, childConfig.URL)
		if childConfig.RootCAFile != "" {
			log.Printf("@I   Using CA certificate: %s\n", childConfig.RootCAFile)
		}
	}

	if Config.Prometheus.Enabled {
		log.Printf("@I Metrics enabled at %v\n", Config.Prometheus.Endpoint)
		http.Handle(Config.Prometheus.Endpoint, promhttp.Handler())
	}

	client = KubeClient{}
	_, err := client.GetTraefikConfiguration()
	if err != nil {
		log.Printf("@W Warning getting first configuration: %v\n", err)
		// Don't exit if we have child controllers configured
		if len(childControllers) == 0 {
			log.Println("@E Error getting first configuration and no child controllers - Exiting")
			os.Exit(1)
		}
	}

	http.HandleFunc(Config.Health.Endpoint, HealthActuator)
	http.HandleFunc("/", MainHandler)

	log.Printf("@I Serving on port %v\n", Config.Port)
	log.Fatal(http.ListenAndServe(":"+Config.Port, nil))
}
