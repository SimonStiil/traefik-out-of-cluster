package main

import (
	"fmt"
	"time"

	traefikV1alpha1 "github.com/traefik/traefik/v3/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
	coreV1 "k8s.io/api/core/v1"
	networkV1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func initCachedKubeClient(kubeClient *KubeClient) *CachedKubeClient {
	return &CachedKubeClient{
		kubeClient:     kubeClient,
		IngressClassIP: make(map[string]IngressClassIP),
		cacheTime:      5,
		exportedLabel:  fmt.Sprintf("%v=%v", LableExported, ExportedTrue),
	}
}

type CachedKubeClient struct {
	kubeClient          *KubeClient
	IngressClassIP      map[string]IngressClassIP
	IngressList         *IngressList
	IngressRouteList    *IngressRouteList
	IngressRouteTCPList *IngressRouteTCPList
	IngressRouteUDPList *IngressRouteUDPList
	cacheTime           float64
	exportedLabel       string
}

type IngressClassIP struct {
	parent *CachedKubeClient
	last   []coreV1.LoadBalancerIngress
	age    time.Time
}

type IngressList struct {
	parent *CachedKubeClient
	last   *networkV1.IngressList
	age    time.Time
}

type IngressRouteList struct {
	parent *CachedKubeClient
	last   *traefikV1alpha1.IngressRouteList
	age    time.Time
}

type IngressRouteTCPList struct {
	parent *CachedKubeClient
	last   *traefikV1alpha1.IngressRouteTCPList
	age    time.Time
}

type IngressRouteUDPList struct {
	parent *CachedKubeClient
	last   *traefikV1alpha1.IngressRouteUDPList
	age    time.Time
}

func (cached *CachedKubeClient) GetIngressClassIP(classname string) ([]coreV1.LoadBalancerIngress, error) {
	class, ok := cached.IngressClassIP[classname]
	if !ok {
		class = IngressClassIP{
			parent: cached,
		}
	}
	if time.Now().Sub(class.age).Seconds() > cached.cacheTime {
		loadBalancerIngress, err := class.getIngressClassIP(classname)
		if err != nil {
			return []coreV1.LoadBalancerIngress{}, err
		}

		class.age = time.Now()
		class.last = loadBalancerIngress
	}
	return class.last, nil
}

func (class *IngressClassIP) getIngressClassIP(classname string) ([]coreV1.LoadBalancerIngress, error) {
	ingressclass, err := class.parent.kubeClient.k8sClient.NetworkingV1().IngressClasses().Get(class.parent.kubeClient.context, classname, metav1.GetOptions{})
	if err != nil {
		return []coreV1.LoadBalancerIngress{}, err
	}
	namespace := ingressclass.ObjectMeta.Annotations["meta.helm.sh/release-namespace"]
	name := ingressclass.ObjectMeta.Annotations["meta.helm.sh/release-name"]
	service, err := class.parent.kubeClient.k8sClient.CoreV1().Services(namespace).Get(class.parent.kubeClient.context, name, metav1.GetOptions{})
	if err != nil {
		return []coreV1.LoadBalancerIngress{}, err
	}
	return service.Status.LoadBalancer.Ingress, nil
}

func (cached *CachedKubeClient) GetIngressList() (*networkV1.IngressList, error) {
	// fmt.Sprintf("%v=%v",LableExported,ExportedTrue)
	class := cached.IngressList
	if cached.IngressList != nil {
		class = &IngressList{
			parent: cached,
		}
	}
	if time.Now().Sub(class.age).Seconds() > cached.cacheTime {
		ingressList, err := class.getIngressList()
		if err != nil {
			return &networkV1.IngressList{}, err
		}

		class.age = time.Now()
		class.last = ingressList
	}
	return class.last, nil
}

func (class *IngressList) getIngressList() (*networkV1.IngressList, error) {
	return class.parent.kubeClient.k8sClient.NetworkingV1().Ingresses("").List(
		class.parent.kubeClient.context,
		metav1.ListOptions{LabelSelector: class.parent.exportedLabel})
}

func (cached *CachedKubeClient) GetIngressRouteList() (*traefikV1alpha1.IngressRouteList, error) {
	// fmt.Sprintf("%v=%v",LableExported,ExportedTrue)
	class := cached.IngressRouteList
	if cached.IngressRouteList != nil {
		class = &IngressRouteList{
			parent: cached,
		}
	}
	if time.Now().Sub(class.age).Seconds() > cached.cacheTime {
		ingressList, err := class.getIngressRouteList()
		if err != nil {
			return &traefikV1alpha1.IngressRouteList{}, err
		}

		class.age = time.Now()
		class.last = ingressList
	}
	return class.last, nil
}

func (class *IngressRouteList) getIngressRouteList() (*traefikV1alpha1.IngressRouteList, error) {
	return class.parent.kubeClient.traefikClient.TraefikV1alpha1().IngressRoutes("").List(
		class.parent.kubeClient.context,
		metav1.ListOptions{LabelSelector: class.parent.exportedLabel})
}

func (cached *CachedKubeClient) GetIngressRouteTCPList() (*traefikV1alpha1.IngressRouteTCPList, error) {
	// fmt.Sprintf("%v=%v",LableExported,ExportedTrue)
	class := cached.IngressRouteTCPList
	if cached.IngressRouteTCPList != nil {
		class = &IngressRouteTCPList{
			parent: cached,
		}
	}
	if time.Now().Sub(class.age).Seconds() > cached.cacheTime {
		ingressRouteList, err := class.getIngressRouteTCPList()
		if err != nil {
			return &traefikV1alpha1.IngressRouteTCPList{}, err
		}

		class.age = time.Now()
		class.last = ingressRouteList
	}
	return class.last, nil
}

func (class *IngressRouteTCPList) getIngressRouteTCPList() (*traefikV1alpha1.IngressRouteTCPList, error) {
	return class.parent.kubeClient.traefikClient.TraefikV1alpha1().IngressRouteTCPs("").List(
		class.parent.kubeClient.context,
		metav1.ListOptions{LabelSelector: class.parent.exportedLabel})
}

func (cached *CachedKubeClient) GetIngressRouteUDPList() (*traefikV1alpha1.IngressRouteUDPList, error) {
	// fmt.Sprintf("%v=%v",LableExported,ExportedTrue)
	class := cached.IngressRouteUDPList
	if cached.IngressRouteUDPList != nil {
		class = &IngressRouteUDPList{
			parent: cached,
		}
	}
	if time.Now().Sub(class.age).Seconds() > cached.cacheTime {
		ingressRouteUDPList, err := class.getIngressRouteUDPList()
		if err != nil {
			return &traefikV1alpha1.IngressRouteUDPList{}, err
		}

		class.age = time.Now()
		class.last = ingressRouteUDPList
	}
	return class.last, nil
}

func (class *IngressRouteUDPList) getIngressRouteUDPList() (*traefikV1alpha1.IngressRouteUDPList, error) {
	return class.parent.kubeClient.traefikClient.TraefikV1alpha1().IngressRouteUDPs("").List(
		class.parent.kubeClient.context,
		metav1.ListOptions{LabelSelector: class.parent.exportedLabel})
}
