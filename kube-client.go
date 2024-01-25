package main

import (
	"context"
	"fmt"
	"log"
	"time"

	traefikconfig "github.com/traefik/traefik/v3/pkg/config/dynamic"
	traefiktls "github.com/traefik/traefik/v3/pkg/tls"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	// https://github.dev/traefik/traefik/blob/master/pkg/provider/kubernetes/gateway/kubernetes.go
	gateclientset "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"

	//gatev1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	// https://github.dev/traefik/traefik/blob/master/pkg/provider/kubernetes/crd/kubernetes.go
	traefikclientset "github.com/traefik/traefik/v3/pkg/provider/kubernetes/crd/generated/clientset/versioned"
	// traefikv1alpha1 "github.com/traefik/traefik/v3/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
)

// Using private functions?
// https://medium.com/@yardenlaif/accessing-private-functions-methods-types-and-variables-in-go-951acccc05a6
// http://www.alangpierce.com/blog/2016/03/17/adventures-in-go-accessing-unexported-functions/

type KubeClient struct {
	context                        context.Context
	age                            time.Time
	lastResult                     *traefikconfig.Configuration
	k8sClient                      *kubernetes.Clientset
	traefikClient                  *traefikclientset.Clientset
	gatewayClient                  *gateclientset.Clientset
	nextServiceID                  int
	nextServerTransportID          int
	serviceNamesMap                map[string]*Service
	hostReWriteServersTransportMap map[string]string
	False                          bool
	WarnPrintStaggerCount          map[string]int
}

func (kube *KubeClient) GetTraefikConfiguration() (*traefikconfig.Configuration, error) {
	var err error = nil
	if kube.k8sClient == nil || kube.traefikClient == nil || kube.gatewayClient == nil {
		if Config.Debug {
			log.Println("@D No client defined, creating new client")
		}
		err = kube.newConfig()
		if err != nil {
			log.Println("@E Errer creating client configuration")
			kube.k8sClient = nil
			return nil, err
		}
		kube.lastResult, err = kube.getTraefikConfiguration()
		if err != nil {
			log.Println("@E Errer Getting ingress data, resetting client")
			kube.k8sClient = nil
			return nil, err
		}
		kube.age = time.Now()
	}
	if time.Now().Sub(kube.age).Seconds() > 5 {
		kube.lastResult, err = kube.getTraefikConfiguration()
		if err != nil {
			log.Println("@E Errer Getting ingress data, resetting client")
			kube.k8sClient = nil
			return nil, err
		}
		kube.age = time.Now()
	}
	return kube.lastResult, err
}

func (kube *KubeClient) newConfig() error {
	if kube.context == nil {
		kube.context = context.Background()
	}
	var config *rest.Config
	var err error
	kube.False = false
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
	if kube.k8sClient == nil {
		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return err
		}
		kube.k8sClient = clientset
	}
	if kube.gatewayClient == nil {
		clientset, err := gateclientset.NewForConfig(config)
		if err != nil {
			return err
		}
		kube.gatewayClient = clientset
	}
	if kube.traefikClient == nil {
		clientset, err := traefikclientset.NewForConfig(config)
		if err != nil {
			return err
		}
		kube.traefikClient = clientset
	}
	return nil
}

const (
	CommonName                = "tooc"
	HTTPServiceName           = CommonName + "-http"
	HTTPSServiceName          = CommonName + "-https"
	TCPServiceName            = CommonName + "-tcp-tls"
	ServerTransportName       = CommonName + "-transport"
	LablePrefix               = "tooc.k8s.stiil.dk/"
	LableExported             = LablePrefix + "export"
	ExportedTrue              = "true" // Only handled if true
	LableSSLForwardType       = LablePrefix + "ssl-type"
	SSLForwardTypePassthrough = "passthrough" //Default
	SSLForwardTypeReEncrypt   = "reencrypt"
	LableRewriteHostname      = LablePrefix + "rewrite-hostname" // Free String
)

type Service struct {
	IPAddress        string
	HTTPServiceName  string
	HTTPSServiceName string
	TCPServiceName   string
}

// Considder looking at:
// https://github.com/traefik/traefik/blob/0ee377bc9f036124b063e7abc3f0958d51ace5fb/pkg/provider/kubernetes/ingress/kubernetes.go

// To include Route CRD's
// https://github.com/traefik/traefik/blob/0ee377bc9f036124b063e7abc3f0958d51ace5fb/pkg/provider/kubernetes/crd/kubernetes.go
func (kube *KubeClient) getAppendServiceNames(config *traefikconfig.Configuration, ip string, remoteHost string) *Service {
	servertransportName := kube.getAppendRewriteServersTransport(config, remoteHost)
	remoteHostname := ip
	if servertransportName != "" {
		remoteHostname = remoteHost
	}
	ipTransportName := fmt.Sprintf("%v-%v", remoteHostname, servertransportName)
	_, ok := kube.serviceNamesMap[ipTransportName]
	if !ok {
		CurrentHTTPServiceName := fmt.Sprintf("%v-%v", HTTPServiceName, kube.nextServiceID)
		CurrentHTTPSServiceName := fmt.Sprintf("%v-%v", HTTPSServiceName, kube.nextServiceID)
		CurrentTCPServiceName := fmt.Sprintf("%v-%v", TCPServiceName, kube.nextServiceID)
		kube.serviceNamesMap[ipTransportName] = &Service{
			IPAddress:        ip,
			HTTPServiceName:  CurrentHTTPServiceName,
			HTTPSServiceName: CurrentHTTPSServiceName,
			TCPServiceName:   CurrentTCPServiceName,
		}
		config.HTTP.Services[CurrentHTTPServiceName] = &traefikconfig.Service{
			LoadBalancer: kube.createServersLoadBalancer(remoteHostname, servertransportName, false)}
		config.HTTP.Services[CurrentHTTPSServiceName] = &traefikconfig.Service{
			LoadBalancer: kube.createServersLoadBalancer(remoteHostname, servertransportName, true)}
		tcpLoadbalancer := &traefikconfig.TCPServersLoadBalancer{
			Servers: []traefikconfig.TCPServer{
				{
					Address: fmt.Sprintf("%v:%v", remoteHostname,
						Config.Cluster.Ingress.HTTPS.Port),
				},
			}}
		config.TCP.Services[CurrentTCPServiceName] = &traefikconfig.TCPService{
			LoadBalancer: tcpLoadbalancer}
		kube.nextServiceID += 1
	}
	return kube.serviceNamesMap[ipTransportName]
}
func (kube *KubeClient) staggeredWarnning(name string) {
	if kube.WarnPrintStaggerCount == nil {
		kube.WarnPrintStaggerCount = make(map[string]int)
	}
	WarnPrintStaggerCount, _ := kube.WarnPrintStaggerCount[name]
	if WarnPrintStaggerCount == 0 {
		log.Printf("@W %v lable used but %v is not defined, this may result in issues if forwardedHeaders are trusted\n", LableRewriteHostname, name)
	}
	if WarnPrintStaggerCount < 100 {
		kube.WarnPrintStaggerCount[name] = WarnPrintStaggerCount + 1
	} else {
		kube.WarnPrintStaggerCount[name] = 0
	}
}
func (kube *KubeClient) getLBConfig(servertransportName string, https bool) *PortConfig {
	if servertransportName == "" {
		if !https {
			return &Config.Cluster.Ingress.HTTP
		} else {
			return &Config.Cluster.Ingress.HTTPS
		}
	} else {
		if !https {
			config := &PortConfig{
				Port:     Config.Cluster.Ingress.Alternate.HTTP.Port,
				Protocol: Config.Cluster.Ingress.Alternate.HTTP.Protocol,
			}
			if Config.Debug {
				log.Printf("@D getLBConfig: https:%v %v %+v\n", https, servertransportName, config)
			}
			if config.Port == "" {
				kube.staggeredWarnning("TOOC_CLUSTER_INGRESS_ALT_HTTP_PORT")
				config.Port = Config.Cluster.Ingress.HTTP.Port
			}
			if config.Protocol == "" {
				config.Protocol = Config.Cluster.Ingress.HTTP.Protocol
			}
			return config
		} else {
			config := &PortConfig{
				Port:     Config.Cluster.Ingress.Alternate.HTTPS.Port,
				Protocol: Config.Cluster.Ingress.Alternate.HTTPS.Protocol,
			}
			if Config.Debug {
				log.Printf("@D getLBConfig: https:%v %v %+v\n", https, servertransportName, config)
			}
			if config.Port == "" {
				kube.staggeredWarnning("TOOC_CLUSTER_INGRESS_ALT_HTTPS_PORT")
				config.Port = Config.Cluster.Ingress.HTTPS.Port
			}
			if config.Protocol == "" {
				config.Protocol = Config.Cluster.Ingress.HTTPS.Protocol
			}
			return config
		}
	}
}
func (kube *KubeClient) createServersLoadBalancer(remoteHostname string, servertransportName string, https bool) *traefikconfig.ServersLoadBalancer {
	config := kube.getLBConfig(servertransportName, https)
	if Config.Debug {
		log.Printf("@D createServersLoadBalancer: %v %v %+v\n", remoteHostname, servertransportName, config)
	}
	serverLoadbalander := &traefikconfig.ServersLoadBalancer{
		Servers: []traefikconfig.Server{
			{
				URL: fmt.Sprintf("%v://%v:%v/", config.Protocol, remoteHostname, config.Port),
			},
		},
	}
	if servertransportName != "" {
		serverLoadbalander.ServersTransport = servertransportName
		serverLoadbalander.PassHostHeader = &kube.False
	}
	return serverLoadbalander
}

func (kube *KubeClient) getAppendRewriteServersTransport(config *traefikconfig.Configuration, hostname string) string {
	if hostname == "" {
		return ""
	}
	_, ok := kube.hostReWriteServersTransportMap[hostname]
	if !ok {
		if config.HTTP.ServersTransports == nil {
			config.HTTP.ServersTransports = make(map[string]*traefikconfig.ServersTransport)
		}
		CurrentServerTransportName := fmt.Sprintf("%v-%v", ServerTransportName, kube.nextServerTransportID)
		kube.hostReWriteServersTransportMap[hostname] = CurrentServerTransportName
		config.HTTP.ServersTransports[CurrentServerTransportName] = &traefikconfig.ServersTransport{
			ServerName: hostname,
			RootCAs:    []traefiktls.FileOrContent{traefiktls.FileOrContent(Config.Cluster.RootCAFilename)},
		}
		kube.nextServerTransportID += 1
	}
	return kube.hostReWriteServersTransportMap[hostname]
}

// https://github.com/traefik/traefik/tree/master/pkg/config/dynamic
func (kube *KubeClient) getTraefikConfiguration() (*traefikconfig.Configuration, error) {
	if Config.Debug {
		log.Println("@D getTraefikConfiguration: ")
	}
	// Implement discovery for ingress controller here: kube.client.CoreV1().Services("") set ingressIP
	kube.nextServiceID = 0
	kube.nextServerTransportID = 0
	kube.serviceNamesMap = make(map[string]*Service)
	kube.hostReWriteServersTransportMap = make(map[string]string)
	ingresses, err := kube.k8sClient.NetworkingV1().Ingresses("").List(
		kube.context,
		metav1.ListOptions{
			LabelSelector: fmt.Sprintf(
				"%v=%v",
				LableExported,
				ExportedTrue)})
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
	for i, ingress := range ingresses.Items {
		SSLForwardType, forwardOK := ingress.Labels[LableSSLForwardType]
		if !forwardOK {
			SSLForwardType = SSLForwardTypePassthrough
		}
		NewHostname, _ := ingress.Labels[LableRewriteHostname]

		ip := Config.Cluster.Ingress.Address
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
		name := CommonName + "-" + ingress.ObjectMeta.Namespace + "-" + ingress.ObjectMeta.Name
		if Config.Debug {
			log.Printf("@D %v: %v %v %v \n", i, name, SSLForwardType, NewHostname)
		}
		for id, rule := range ingress.Spec.Rules {
			var currentService *Service
			var currentHostname string
			if NewHostname == "" {
				currentHostname = rule.Host
				currentService = kube.getAppendServiceNames(traefikConfig, ip, "")
			} else {
				currentHostname = NewHostname
				currentService = kube.getAppendServiceNames(traefikConfig, ip, rule.Host)
			}
			// Path Rules example - && Path(`/traefik`))
			traefikConfig.HTTP.Routers[fmt.Sprintf("%v-%v", name, id)] = &traefikconfig.Router{
				EntryPoints: []string{Config.Traefik.HTTP.Entrypoint.Name},
				Rule:        fmt.Sprintf("Host(`%v`)", currentHostname),
				Service:     currentService.HTTPServiceName,
			}
			if SSLForwardType == SSLForwardTypePassthrough {
				traefikConfig.TCP.Routers[fmt.Sprintf("%v-%v-tls", name, id)] = &traefikconfig.TCPRouter{
					EntryPoints: []string{Config.Traefik.HTTPS.Entrypoint.Name},
					Rule:        fmt.Sprintf("HostSNI(`%v`)", currentHostname),
					Service:     currentService.TCPServiceName,
					TLS:         &traefikconfig.RouterTCPTLSConfig{Passthrough: true},
				}
			} else {
				if SSLForwardType == SSLForwardTypeReEncrypt {
					traefikConfig.HTTP.Routers[fmt.Sprintf("%v-%v-tls", name, id)] = &traefikconfig.Router{
						EntryPoints: []string{Config.Traefik.HTTPS.Entrypoint.Name},
						Rule:        fmt.Sprintf("Host(`%v`)", currentHostname),
						Service:     currentService.HTTPSServiceName,
						TLS:         &traefikconfig.RouterTLSConfig{},
					}
				} else {
					log.Printf("@W GetIngresses: Unsupported annotation option %v=%v", LableSSLForwardType, SSLForwardType)
				}
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
