package main

import (
	"context"
	"fmt"
	"log"
	"time"

	traefikconfig "github.com/traefik/traefik/v3/pkg/config/dynamic"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type KubeClient struct {
	context    context.Context
	age        time.Time
	lastResult *traefikconfig.Configuration
	client     *kubernetes.Clientset
}

func (kube *KubeClient) CetClusters() (*traefikconfig.Configuration, error) {
	var err error = nil
	if kube.client == nil {
		if DynamicConfig.GetBool("Debug") {
			log.Println("@D No client defined, creating new client")
		}
		err = kube.newConfig()
		if err != nil {
			log.Println("@E Errer creating client configuration")
			kube.client = nil
			return nil, err
		}
		kube.lastResult, err = kube.getClusters()
		if err != nil {
			log.Println("@E Errer Getting cluster data, resetting client")
			kube.client = nil
			return nil, err
		}
		kube.age = time.Now()
	}
	if time.Now().Sub(kube.age).Seconds() > 5 {
		kube.lastResult, err = kube.getClusters()
		if err != nil {
			log.Println("@E Errer Getting cluster data, resetting client")
			kube.client = nil
			return nil, err
		}
		kube.age = time.Now()
	}
	return kube.lastResult, err
}

func (kube *KubeClient) newConfig() error {
	var config *rest.Config
	var err error
	if Kubeconfig != "" {
		log.Printf("@I Using kubeconfig in: %v\n", Kubeconfig)
		config, err = clientcmd.BuildConfigFromFlags("", Kubeconfig)
		if err != nil {
			return err
		}
	} else {
		config, err = rest.InClusterConfig()
		log.Println("@I Using in Cluster Configuration")
		if err != nil {
			return err
		}
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	kube.context = context.Background()
	kube.client = clientset
	return nil
}

const (
	CommonName      = "tooc"
	HTTPServiceName = CommonName + "-ingress-http"
	TCPServiceName  = CommonName + "-ingress-tcp-tls"
)

func (kube *KubeClient) getClusters() (*traefikconfig.Configuration, error) {
	if DynamicConfig.GetBool("Debug") {
		log.Println("@D getClusters: ")
	}
	// Implement discovery for ingress controller here: kube.client.CoreV1().Services("") set ingressIP

	ingresses, err := kube.client.NetworkingV1().Ingresses("").List(kube.context, metav1.ListOptions{LabelSelector: "export=true"})
	if err != nil {
		return nil, err
	}
	httpConfig := &traefikconfig.HTTPConfiguration{Services: make(map[string]*traefikconfig.Service), Routers: make(map[string]*traefikconfig.Router)}
	httpConfig.Services[HTTPServiceName] = &traefikconfig.Service{
		LoadBalancer: &traefikconfig.ServersLoadBalancer{
			Servers: []traefikconfig.Server{
				{
					URL: fmt.Sprintf("http://%v:%v/",
						DynamicConfig.GetString("Cluster.IngressIP"),
						DynamicConfig.GetString("Traefik.HTTP.Entrypoint.Port")),
				},
			}}}
	tcpConfig := &traefikconfig.TCPConfiguration{Services: make(map[string]*traefikconfig.TCPService), Routers: make(map[string]*traefikconfig.TCPRouter)}
	tcpConfig.Services[TCPServiceName] = &traefikconfig.TCPService{
		LoadBalancer: &traefikconfig.TCPServersLoadBalancer{
			Servers: []traefikconfig.TCPServer{
				{
					Address: fmt.Sprintf("%v:%v",
						DynamicConfig.GetString("Cluster.IngressIP"),
						DynamicConfig.GetString("Traefik.HTTPS.Entrypoint.Port")),
				},
			}}}
	total_rules := 0
	for _, ingress := range ingresses.Items {
		name := CommonName + ingress.ObjectMeta.Namespace + "-" + ingress.ObjectMeta.Name
		for id, rule := range ingress.Spec.Rules {
			hostname := rule.Host
			httpConfig.Routers[fmt.Sprintf("%v-%v", name, id)] = &traefikconfig.Router{
				EntryPoints: []string{DynamicConfig.GetString("Traefik.HTTP.Entrypoint.Name")},
				Rule:        fmt.Sprintf("Host(`%v`)", hostname),
				Service:     HTTPServiceName,
			}
			tcpConfig.Routers[fmt.Sprintf("%v-%v", name, id)] = &traefikconfig.TCPRouter{
				EntryPoints: []string{DynamicConfig.GetString("Traefik.HTTPS.Entrypoint.Name")},
				Rule:        fmt.Sprintf("HostSNI(`%v`)", hostname),
				Service:     TCPServiceName,
				TLS:         &traefikconfig.RouterTCPTLSConfig{Passthrough: true},
			}
			total_rules += 1
		}
	}
	traefikConfig := &traefikconfig.Configuration{HTTP: httpConfig, TCP: tcpConfig}
	if DynamicConfig.GetBool("Prometheus.Enabled") {
		exported_ingress_count.Set(float64(len(ingresses.Items)))
		routes_created_count.Set(float64(total_rules))

	}
	return traefikConfig, nil
}
