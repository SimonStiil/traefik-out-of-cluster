package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"
	"k8s.io/client-go/util/homedir"
)

var (
	Config ConfigType
	//DynamicConfig viper.Viper
	Kubeconfig string
	requests   = promauto.NewCounterVec(prometheus.CounterOpts{
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
	clusterList, err := client.GetTraefikConfiguration()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 Internal Server Error"))
		log.Printf("@I %v %v %v %v - Main Handler Request Error - %+v\n", r.Method, r.URL.Path, r.RemoteAddr, 500, err.Error())
		return
	}
	if Config.Debug || Config.Print.Ok {
		log.Printf("@I %v %v %v %v - Main Handler\n", r.Method, r.URL.Path, r.RemoteAddr, 200)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clusterList)
	return
}

type ConfigType struct {
	Debug      bool             `mapstructure:"Debug"`
	Print      PrintDebug       `mapstructure:"Print"`
	Port       string           `mapstructure:"Port"`
	Cluster    ClusterConfig    `mapstructure:"Cluster"`
	Traefik    TraefikConfig    `mapstructure:"Traefik"`
	Prometheus PrometheusConfig `mapstructure:"Prometheus"`
	Health     HealthConfig     `mapstructure:"Health"`
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

func main() {
	DynamicConfig := *viper.New()
	//DynamicConfig.SetEnvPrefix("TOOC")
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
	/*
		if Config.Cluster.Ingress.Address == "" {
			log.Println("@E Config option TOOC_CLUSTER_INGRESS_ADDRESS not defined - Exiting")
			os.Exit(1)
		}
	*/
	Kubeconfig = Config.Cluster.Kubeconfig
	if Kubeconfig == "" {
		if home := homedir.HomeDir(); home != "" {
			homeConfig := filepath.Join(home, ".kube", "config")
			if _, err := os.Stat(homeConfig); err == nil {
				Kubeconfig = homeConfig
			}
		}
	}
	if Config.Prometheus.Enabled {
		log.Printf("@I Metrics enabled at %v\n", Config.Prometheus.Endpoint)
		http.Handle(Config.Prometheus.Endpoint, promhttp.Handler())
	}

	client = KubeClient{}
	_, err := client.GetTraefikConfiguration()
	if err != nil {
		log.Println("@E Error getting first configuration - Exiting")
		os.Exit(1)
	}

	http.HandleFunc(Config.Health.Endpoint, HealthActuator)
	http.HandleFunc("/", MainHandler)

	log.Printf("@I Serving on port %v\n", Config.Port)
	log.Fatal(http.ListenAndServe(":"+Config.Port, nil))
}
