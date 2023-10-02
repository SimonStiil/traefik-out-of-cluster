package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"

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
		log.Printf("@I %v %v %v - HealthActuator\n", r.Method, r.URL.Path, 404)
		http.NotFoundHandler().ServeHTTP(w, r)
		return
	}
	reply := Health{Status: "UP"}
	log.Printf("@I %v %v %v - HealthActuator\n", r.Method, r.URL.Path, 200)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
	return
}

func MainHandler(w http.ResponseWriter, r *http.Request) {
	if Config.Prometheus.Enabled {
		requests.WithLabelValues(r.URL.EscapedPath(), r.Method).Inc()
	}
	clusterList, err := client.CetTraefikConfiguration()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 Internal Server Error"))
		log.Printf("@I %v %v %v - Main Handler Request Error - %+v\n", r.Method, r.URL.Path, 500, err.Error())
		return
	}
	log.Printf("@I %v %v %v - Main Handler\n", r.Method, r.URL.Path, 200)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clusterList)
	return
}

type ConfigType struct {
	Debug      bool             `mapstructure:"Debug"`
	Port       string           `mapstructure:"Port"`
	Cluster    ClusterConfig    `mapstructure:"Cluster"`
	Traefik    TraefikConfig    `mapstructure:"Traefik"`
	Prometheus PrometheusConfig `mapstructure:"Prometheus"`
	Health     HealthConfig     `mapstructure:"Health"`
}
type ClusterConfig struct {
	IngressIP  string `mapstructure:"IngressIP"`
	Kubeconfig string `mapstructure:"Kubeconfig"`
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
	Port string `mapstructure:"Port"`
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
	DynamicConfig.SetEnvPrefix("TOOC")
	DynamicConfig.SetDefault("Debug", false)
	DynamicConfig.SetDefault("Port", 8080)
	DynamicConfig.SetDefault("Cluster.IngressIP", "192.168.1.20")
	DynamicConfig.SetDefault("Cluster.Kubeconfig", "")
	DynamicConfig.SetDefault("Traefik.HTTP.Entrypoint.Name", "web")
	DynamicConfig.SetDefault("Traefik.HTTP.Entrypoint.Port", 80)
	DynamicConfig.SetDefault("Traefik.HTTPS.Entrypoint.Name", "websecure")
	DynamicConfig.SetDefault("Traefik.HTTPS.Entrypoint.Port", 443)
	DynamicConfig.SetDefault("Prometheus.Enabled", true)
	DynamicConfig.SetDefault("Prometheus.Endpoint", "/metrics")
	DynamicConfig.SetDefault("Health.Endpoint", "/health")
	DynamicConfig.AutomaticEnv()
	DynamicConfig.Unmarshal(&Config)
	if Config.Debug {
		log.Println("@D Debugging enabled")
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
	if Config.Prometheus.Enabled {
		log.Printf("@I Metrics enabled at %v\n", Config.Prometheus.Endpoint)
		http.Handle(Config.Prometheus.Endpoint, promhttp.Handler())
	}

	client = KubeClient{}
	http.HandleFunc(Config.Health.Endpoint, HealthActuator)
	http.HandleFunc("/", MainHandler)

	log.Printf("@I Serving on port %v\n", Config.Port)
	log.Fatal(http.ListenAndServe(":"+Config.Port, nil))
}
