# Traefik out of Cluster
This is a tool to help out in the situation where you want to use traefik as a loadbalancer for *some* ingress objects.  
Traefik has the ability to be used as many provider types described in [Configuration discovery - Overview.](https://doc.traefik.io/traefik/providers/overview/)  
But sometimes there are situations where you want to have a 2nd layer of loadbalancer. 

It can be internally in a cluster ( Pod -> Service -> Internal Loadbalancer -> Cluster Loadbalancer )  
It can be externally for the organization ( Pod -> Service -> Cluster Loadbalancer -> Organization Loadbalancer )  
But setting up all these layers of loadbalancers manually is not fun.

This is where this tool comes in.

It allows you to "overload" ingress objects with a label `tooc.k8s.stiil.dk/external=true` picks up the Loadbalancer IP and hostname, generates a JSON configuration for that configuration and acts as a [HTTP Configuration provider](https://doc.traefik.io/traefik/providers/overview/) for Traefik.

Additional options are available with:  
`tooc.k8s.stiil.dk/ssl-type=passthrough` (default option) for tls passthrough  
`tooc.k8s.stiil.dk/ssl-type=reencrypt` allows for tls reencrypt at external Traefik instance. Prerequicit for working with ingress at external Traefik instance  
`tooc.k8s.stiil.dk/rewrite-hostname=[external hostname]` Set the hostname of the external rule, this requires a [special configuration](#Special-Requisits-for-Hostname-rewrite-hostname)  

## Planed feature improvements
* Addition of Paths
* Allow for extra traefik options (Middle wares)
* Gateway API Support
* Helm Chart

# Download
Docker image can be fetched from [dockerhub simonstiil/traefik-out-of-cluster](https://hub.docker.com/repository/docker/simonstiil/traefik-out-of-cluster)

Can be build with `go build .`

Example configuration can be found i [here](./deployment/) 

# Configuration
Configuration is done throug Environment variables

| Option | Description(Defaults) |
| ------ | ----------- |
| TOOC_DEBUG | Enable debugging output (developer focused) |
| TOOC_PRINT_OK | Enable printing 200 ok statements to log (helps with debugging) |
| TOOC_PORT | Port for service (8080) |
| TOOC_CLUSTER_KUBECONFIG | Path to Kubeconfig will autodescover in home or service account in cluster |
| TOOC_CLUSTER_INGRESS_ADDRESS | REQUIRED IP to use if unable to determin ip internally from Ingress Status  |
| TOOC_CLUSTER_INGRESS_HTTP_PORT | Loadbalancer port to connect to (80) |
| TOOC_CLUSTER_INGRESS_HTTP_PROTOCOL | Loadbalancer protocol to connect to (http) |
| TOOC_CLUSTER_INGRESS_HTTPS_PORT | Loadbalancer port to connect to (443) |
| TOOC_CLUSTER_INGRESS_HTTPS_PROTOCOL | Loadbalancer protocol to connect to (https) |
| TOOC_CLUSTER_INGRESS_ALT_HTTP_PORT | Loadbalancer port to connect to (Non ALT config) |
| TOOC_CLUSTER_INGRESS_ALT_HTTP_PROTOCOL | Loadbalancer protocol to connect to (Non ALT config) |
| TOOC_CLUSTER_INGRESS_ALT_HTTPS_PORT | Loadbalancer port to connect to (Non ALT config) |
| TOOC_CLUSTER_INGRESS_ALT_HTTPS_PROTOCOL | Loadbalancer protocol to connect to (Non ALT config) |
| TOOC_TRAEFIK_HTTP_ENTRYPOINT_NAME | Entrypoint name to bind to for HTTP (web) |
| TOOC_TRAEFIK_HTTPS_ENTRYPOINT_NAME | Entrypoint name to bind to for HTTP (websecure) |
| TOOC_PROMETHEUS_ENABLED | Enable prometheus endpoint (true) |
| TOOC_PROMETHEUS_ENDPOINT | Path where to find prometheus endpoint (/metrics) |
| TOOC_HEALTH_ENDPOINT | Path where to find health endpoint (/health) |

## Special Requisits for Hostname 'rewrite-hostname'
Due to traefik being very "Helpful" it will always forward the following set of headders  
``` yaml
X-Forwarded-For: [Connecting IP, If information is forwarded]
X-Forwarded-Host: [External Hostname]
X-Forwarded-Port: [External Port]
X-Forwarded-Proto: [External protocol]
X-Forwarded-Server: [External Server Hostname]
X-Real-Ip: [Connecting IP, If information is forwarded]
```

This would usually be good as it helps improve security that you get the original ip and host.
But problem is Traefik also useses `X-Forwarded-Host` rather then `Host` when it is setup with `forwardedHeaders.trustedIPs` or `forwardedHeaders.insecure`.

So if you want to use 'rewrite-hostname' you need to have an entrypoint with forwardedHeaders disabled :-/ and setup TOOC_CLUSTER_INGRESS_ALT_HTTP_PORT, TOOC_CLUSTER_INGRESS_ALT_HTTPS_PORT Configuration

Example Traefik ingress helm configuration (Specific for this):
``` yaml
additionalArguments:
- --entryPoints.web.forwardedHeaders.trustedIPs=127.0.0.1/32,192.168.1.5
- --entryPoints.websecure.forwardedHeaders.trustedIPs=127.0.0.1/32,192.168.1.5
# External Ingress controller located at 192.168.1.5
# Notise no definition for entrypoints webnofwd and websecurenofwd
ports:
  webnofwd:
    port: 7080
    expose: true
    exposedPort: 7080
    protocol: TCP
    middlewares: []
  websecurenofwd:
    port: 7443
    expose: true
    exposedPort: 7443
    protocol: TCP
    http3:
      enabled: false
    tls:
      enabled: true
      options: ""
      certResolver: ""
      domains: []
    middlewares: []
```
The configuration for the Traefik out of cluster would then be, for a cluster running with ingress controller on `192.168.1.6`:

```bash
TOOC_CLUSTER_INGRESS_ALT_HTTP_PORT=7080
TOOC_CLUSTER_INGRESS_ALT_HTTPS_PORT=7443
TOOC_CLUSTER_INGRESS_ADDRESS=192.168.1.6
```

## Example Traefik configuration generated from Single ingress
```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    kubernetes.io/ingress.class: traefik-traefik
    traefik.ingress.kubernetes.io/router.tls: "true"
  labels:
    app: whoami
    export: "true"
  name: whoami
  namespace: test
spec:
  rules:
  - host: whoami.k3s.home
    http:
      paths:
      - backend:
          service:
            name: whoami
            port:
              number: 80
        path: /
        pathType: Prefix
status:
  loadBalancer:
    ingress:
    - ip: 192.168.1.20
```

```json
{
  "http":{
    "routers":{
      "tooc-test-whoami-0":{
        "entryPoints":[
          "web"
        ],
        "service":"tooc-http-0",
        "rule":"Host(`whoami.k3s.home`)"
      }
    },
    "services":{
      "tooc-http-0":{
        "loadBalancer":{
          "servers":[
            {
              "url":"http://192.168.1.20:80/"
            }
          ],
          "passHostHeader":null
        }
      }
    }
  },
  "tcp":{
    "routers":{
      "tooc-test-whoami-0":{
        "entryPoints":[
          "web"
        ],
        "service":"tooc-tcp-tls-0",
        "rule":"HostSNI(`whoami.k3s.home`)",
        "tls":{
          "passthrough":true
        }
      }
    },
    "services":{
      "tooc-tcp-tls-0":{
        "loadBalancer":{
          "servers":[
            {
              "address":"192.168.1.20:443"
            }
          ]
        }
      }
    }
  }
}

```

## Example External Traefik configuration
```yaml
providers:
  http:
    endpoint: "https://traefik-out-of-cluster.k3s.local/"
    pollInterval: "15s" # Default value is 5 sec, choose what makes sense to you
    pollTimeout: "5s" # Default value is 5 sec, choose what makes sense to you
    tls: # For mTLS
      cert: path/to/mtls.cert
      key: path/to/mtls.key
    
```
See [Certificates](./certificates/) for example mTLS setup of ingress using Traefik ingress controller
