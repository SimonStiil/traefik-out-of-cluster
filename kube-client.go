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
	context         context.Context
	age             time.Time
	lastResult      *traefikconfig.Configuration
	client          *kubernetes.Clientset
	nextID          int
	serviceNamesMap map[string]*Service
}

func (kube *KubeClient) CetTraefikConfiguration() (*traefikconfig.Configuration, error) {
	var err error = nil
	if kube.client == nil {
		if Config.Debug {
			log.Println("@D No client defined, creating new client")
		}
		err = kube.newConfig()
		if err != nil {
			log.Println("@E Errer creating client configuration")
			kube.client = nil
			return nil, err
		}
		kube.lastResult, err = kube.getTraefikConfiguration()
		if err != nil {
			log.Println("@E Errer Getting ingress data, resetting client")
			kube.client = nil
			return nil, err
		}
		kube.age = time.Now()
	}
	if time.Now().Sub(kube.age).Seconds() > 5 {
		kube.lastResult, err = kube.getTraefikConfiguration()
		if err != nil {
			log.Println("@E Errer Getting ingress data, resetting client")
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
	HTTPServiceName = CommonName + "-http"
	TCPServiceName  = CommonName + "-tcp-tls"
)

type Service struct {
	IPAddress       string
	HTTPServiceName string
	TCPServiceName  string
}

func (kube *KubeClient) getAppendServiceNames(config *traefikconfig.Configuration, ip string) *Service {
	_, ok := kube.serviceNamesMap[ip]
	if !ok {
		CurrentHTTPServiceName := fmt.Sprintf("%v-%v", HTTPServiceName, kube.nextID)
		CurrentTCPServiceName := fmt.Sprintf("%v-%v", TCPServiceName, kube.nextID)
		kube.serviceNamesMap[ip] = &Service{
			IPAddress:       ip,
			HTTPServiceName: CurrentHTTPServiceName,
			TCPServiceName:  CurrentTCPServiceName,
		}
		config.HTTP.Services[CurrentHTTPServiceName] = &traefikconfig.Service{
			LoadBalancer: &traefikconfig.ServersLoadBalancer{
				Servers: []traefikconfig.Server{
					{
						URL: fmt.Sprintf("http://%v:%v/", ip,
							Config.Traefik.HTTP.Entrypoint.Port),
					},
				}}}
		config.TCP.Services[CurrentTCPServiceName] = &traefikconfig.TCPService{
			LoadBalancer: &traefikconfig.TCPServersLoadBalancer{
				Servers: []traefikconfig.TCPServer{
					{
						Address: fmt.Sprintf("%v:%v", ip,
							Config.Traefik.HTTPS.Entrypoint.Port),
					},
				}}}
		kube.nextID += 1
	}
	return kube.serviceNamesMap[ip]
}

// https://github.com/traefik/traefik/tree/master/pkg/config/dynamic
func (kube *KubeClient) getTraefikConfiguration() (*traefikconfig.Configuration, error) {
	if Config.Debug {
		log.Println("@D getTraefikConfiguration: ")
	}
	// Implement discovery for ingress controller here: kube.client.CoreV1().Services("") set ingressIP
	kube.nextID = 0
	kube.serviceNamesMap = make(map[string]*Service)
	ingresses, err := kube.client.NetworkingV1().Ingresses("").List(kube.context, metav1.ListOptions{LabelSelector: "export=true"})
	if err != nil {
		return nil, err
	}
	if Config.Debug {
		log.Printf("@D getTraefikConfiguration: found %v exported ingresses", len(ingresses.Items))
	}
	traefikConfig := &traefikconfig.Configuration{
		HTTP: &traefikconfig.HTTPConfiguration{
			Services: make(map[string]*traefikconfig.Service),
			Routers:  make(map[string]*traefikconfig.Router)},
		TCP: &traefikconfig.TCPConfiguration{
			Services: make(map[string]*traefikconfig.TCPService),
			Routers:  make(map[string]*traefikconfig.TCPRouter)},
	}
	total_rules := 0
	broken_rules := 0
	for _, ingress := range ingresses.Items {
		ip := Config.Cluster.IngressIP
		// https://pkg.go.dev/k8s.io/api/networking/v1#Ingress
		if len(ingress.Status.LoadBalancer.Ingress) > 0 {
			ip = ingress.Status.LoadBalancer.Ingress[0].IP
		} else {
			log.Printf("@I getTraefikConfiguration: ingress %v %v did not contain loadbalancer IP, reverting to default\n", ingress.ObjectMeta.Namespace, ingress.ObjectMeta.Name)
		}
		if ip == "" {
			log.Println("@E getTraefikConfiguration: default ip not set aborting")
			broken_rules += 1
			continue
		}
		Service := kube.getAppendServiceNames(traefikConfig, ip)
		name := CommonName + "-" + ingress.ObjectMeta.Namespace + "-" + ingress.ObjectMeta.Name
		for id, rule := range ingress.Spec.Rules {
			hostname := rule.Host
			traefikConfig.HTTP.Routers[fmt.Sprintf("%v-%v", name, id)] = &traefikconfig.Router{
				EntryPoints: []string{Config.Traefik.HTTP.Entrypoint.Name},
				Rule:        fmt.Sprintf("Host(`%v`)", hostname),
				Service:     Service.HTTPServiceName,
			}
			traefikConfig.TCP.Routers[fmt.Sprintf("%v-%v", name, id)] = &traefikconfig.TCPRouter{
				EntryPoints: []string{Config.Traefik.HTTPS.Entrypoint.Name},
				Rule:        fmt.Sprintf("HostSNI(`%v`)", hostname),
				Service:     Service.TCPServiceName,
				TLS:         &traefikconfig.RouterTCPTLSConfig{Passthrough: true},
			}
			total_rules += 1
		}
	}
	if Config.Prometheus.Enabled {
		exported_ingress_count.Set(float64(len(ingresses.Items)))
		routes_created_count.Set(float64(total_rules))
		broken_ingress_count.Set(float64(broken_rules))
	}
	return traefikConfig, nil
}
