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
	//	config        Config
	DynamicConfig viper.Viper
	Kubeconfig    string
	/*
		debug              bool
		prometheusEnabled  bool
		prometheusEndpoint string
		healthEndpoint     string
		kubeconfig         string
		port               string
		ingressIP          string
		web                string
		webPort            string
		websecure          string
		websecurePort      string
		onlyRootEndpoint   bool
	*/
	requests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_endpoint_equests_count",
		Help: "The amount of requests to an endpoint",
	}, []string{"endpoint", "method"},
	)
	exported_ingress_count = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "exported_ingress_count",
		Help: "Amount of exported ingresses found in cluster"})
	routes_created_count = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "routes_created_count",
		Help: "Amount of routes created in the config"})

	client KubeClient
)

type Health struct {
	Status string `json:"status"`
}

func HealthActuator(w http.ResponseWriter, r *http.Request) {
	if DynamicConfig.GetBool("Prometheus.Enabled") {
		requests.WithLabelValues(r.URL.EscapedPath(), r.Method).Inc()
	}
	if !(r.URL.Path == DynamicConfig.GetString("Health.Endpoint")) {
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
	if DynamicConfig.GetBool("Prometheus.Enabled") {
		requests.WithLabelValues(r.URL.EscapedPath(), r.Method).Inc()
	}
	clusterList, err := client.CetClusters()
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

/*
type Config struct {
	Debug              bool   `mapstructure:"TOOC_DEBUG"`
	Port               string `mapstructure:"TOOC_PORT"`
	IngressIP          string `mapstructure:"TOOC_CLUSTER_INGRESS_IP"`
	Kubeconfig         string `mapstructure:"TOOC_CLUSTER_KUBECONFIG"`
	HTTPEndpointName   string `mapstructure:"TOOC_TRAEFIK_HTTP_ENRYPOINT_NAME"`
	HTTPEndpointPort   string `mapstructure:"TOOC_TRAEFIK_HTTP_ENRYPOINT_PORT"`
	HTTPSEndpointName  string `mapstructure:"TOOC_TRAEFIK_HTTPS_ENRYPOINT_NAME"`
	HTTPSEndpointPort  string `mapstructure:"TOOC_TRAEFIK_HTTPS_ENRYPOINT_PORT"`
	PrometheusEnabled  bool   `mapstructure:"TOOC_PROMETHEUS_ENABLED"`
	PrometheusEndpoint string `mapstructure:"TOOC_PROMETHEUS_ENDPOINT"`
	HealthEndpoint     string `mapstructure:"TOOC_HEALTH_ENDPOINT"`
}
func LoadConfig(path string) (config Config, err error) {
	viper.AddConfigPath(path)
	viper.SetConfigName("app")
	viper.SetConfigType("env")

	viper.AutomaticEnv()

	err = viper.ReadInConfig()
	if err != nil {
		return
	}

	err = viper.Unmarshal(&config)
	return
}
*/

func main() {
	DynamicConfig = *viper.New()
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
	/*
		err := DynamicConfig.ReadInConfig()
		if err != nil {
			log.Fatalf("Error loading config: %+v", err)
		}
	*/
	if DynamicConfig.GetBool("Debug") {
		log.Println("@D Debugging enabled")
	}
	Kubeconfig = DynamicConfig.GetString("Cluster.Kubeconfig")
	if Kubeconfig == "" {
		if home := homedir.HomeDir(); home != "" {
			homeConfig := filepath.Join(home, ".kube", "config")
			if _, err := os.Stat(homeConfig); err == nil {
				Kubeconfig = homeConfig
			}
		}
	}

	if DynamicConfig.GetBool("Prometheus.Enabled") {
		log.Printf("@I Metrics enabled at %v\n", DynamicConfig.GetString("Prometheus.Endpoint"))
		http.Handle(DynamicConfig.GetString("Prometheus.Endpoint"), promhttp.Handler())
	}

	client = KubeClient{}
	http.HandleFunc(DynamicConfig.GetString("Health.Endpoint"), HealthActuator)
	http.HandleFunc("/", MainHandler)

	log.Printf("@I Serving on port %v\n", DynamicConfig.GetString("Port"))
	log.Fatal(http.ListenAndServe(":"+DynamicConfig.GetString("Port"), nil))
}
