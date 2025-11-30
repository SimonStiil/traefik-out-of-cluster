package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	traefikconfig "github.com/traefik/traefik/v3/pkg/config/dynamic"
)

type ChildController struct {
	Name       string
	URL        string
	Timeout    time.Duration
	RootCAFile string
	lastFetch  time.Time
	lastConfig *traefikconfig.Configuration
}

// FetchConfiguration fetches the Traefik configuration from a child controller
func (c *ChildController) FetchConfiguration(ctx context.Context) (*traefikconfig.Configuration, error) {
	if Config.Debug {
		log.Printf("@D Fetching configuration from child controller: %s (%s)\n", c.Name, c.URL)
	}

	// Create HTTP client with optional custom CA
	tlsConfig := &tls.Config{}

	if c.RootCAFile != "" {
		caCert, err := os.ReadFile(c.RootCAFile)
		if err != nil {
			if Config.Prometheus.Enabled {
				child_fetch_errors.WithLabelValues(c.Name).Inc()
			}
			return nil, fmt.Errorf("reading CA certificate %s: %w", c.RootCAFile, err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			if Config.Prometheus.Enabled {
				child_fetch_errors.WithLabelValues(c.Name).Inc()
			}
			return nil, fmt.Errorf("failed to parse CA certificate from %s", c.RootCAFile)
		}

		tlsConfig.RootCAs = caCertPool
		if Config.Debug {
			log.Printf("@D Using custom CA certificate for child %s: %s\n", c.Name, c.RootCAFile)
		}
	}

	client := &http.Client{
		Timeout: c.Timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", c.URL, nil)
	if err != nil {
		if Config.Prometheus.Enabled {
			child_fetch_errors.WithLabelValues(c.Name).Inc()
		}
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		if Config.Prometheus.Enabled {
			child_fetch_errors.WithLabelValues(c.Name).Inc()
		}
		return nil, fmt.Errorf("fetching from %s: %w", c.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if Config.Prometheus.Enabled {
			child_fetch_errors.WithLabelValues(c.Name).Inc()
		}
		return nil, fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, c.URL, string(body))
	}

	var config traefikconfig.Configuration
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		if Config.Prometheus.Enabled {
			child_fetch_errors.WithLabelValues(c.Name).Inc()
		}
		return nil, fmt.Errorf("decoding response from %s: %w", c.URL, err)
	}

	c.lastFetch = time.Now()
	c.lastConfig = &config

	if Config.Prometheus.Enabled {
		child_fetch_success.WithLabelValues(c.Name).Inc()
	}

	if Config.Debug {
		log.Printf("@D Successfully fetched configuration from %s\n", c.Name)
	}

	return &config, nil
}

// prefixConfigurationNames adds a namespace prefix to all service and router names
// to prevent naming conflicts when merging multiple configurations
// Format: tooc-{namespace}-{original-name-without-tooc}
func prefixConfigurationNames(config *traefikconfig.Configuration, namespace string) *traefikconfig.Configuration {
	if config == nil {
		return nil
	}

	// Helper to reformat names: tooc-http-0 -> tooc-namespace-http-0
	renameFn := func(name string) string {
		// Remove "tooc-" prefix if present
		name = strings.TrimPrefix(name, CommonName+"-")
		// Add namespace: tooc-namespace-originalname
		return fmt.Sprintf("%s-%s-%s", CommonName, namespace, name)
	}

	prefixed := &traefikconfig.Configuration{
		HTTP: &traefikconfig.HTTPConfiguration{
			Services:          make(map[string]*traefikconfig.Service),
			Routers:           make(map[string]*traefikconfig.Router),
			Middlewares:       make(map[string]*traefikconfig.Middleware),
			ServersTransports: make(map[string]*traefikconfig.ServersTransport),
		},
		TCP: &traefikconfig.TCPConfiguration{
			Services:    make(map[string]*traefikconfig.TCPService),
			Routers:     make(map[string]*traefikconfig.TCPRouter),
			Middlewares: make(map[string]*traefikconfig.TCPMiddleware),
		},
		TLS: config.TLS, // TLS config typically doesn't need prefixing
	}

	// Prefix HTTP services
	if config.HTTP != nil {
		for name, service := range config.HTTP.Services {
			prefixedName := renameFn(name)
			prefixed.HTTP.Services[prefixedName] = service
		}

		// Prefix HTTP routers and update service references
		for name, router := range config.HTTP.Routers {
			prefixedName := renameFn(name)
			prefixedRouter := *router // Copy router
			if router.Service != "" {
				prefixedRouter.Service = renameFn(router.Service)
			}
			prefixed.HTTP.Routers[prefixedName] = &prefixedRouter
		}

		// Prefix HTTP middlewares
		for name, middleware := range config.HTTP.Middlewares {
			prefixedName := renameFn(name)
			prefixed.HTTP.Middlewares[prefixedName] = middleware
		}

		// Prefix HTTP servers transports
		for name, transport := range config.HTTP.ServersTransports {
			prefixedName := renameFn(name)
			prefixed.HTTP.ServersTransports[prefixedName] = transport
		}
	}

	// Prefix TCP services
	if config.TCP != nil {
		for name, service := range config.TCP.Services {
			prefixedName := renameFn(name)
			prefixed.TCP.Services[prefixedName] = service
		}

		// Prefix TCP routers and update service references
		for name, router := range config.TCP.Routers {
			prefixedName := renameFn(name)
			prefixedRouter := *router // Copy router
			if router.Service != "" {
				prefixedRouter.Service = renameFn(router.Service)
			}
			prefixed.TCP.Routers[prefixedName] = &prefixedRouter
		}

		// Prefix TCP middlewares
		for name, middleware := range config.TCP.Middlewares {
			prefixedName := renameFn(name)
			prefixed.TCP.Middlewares[prefixedName] = middleware
		}
	}

	return prefixed
}

// mergeConfigurations combines multiple Traefik configurations into one
func mergeConfigurations(configs ...*traefikconfig.Configuration) *traefikconfig.Configuration {
	merged := &traefikconfig.Configuration{
		HTTP: &traefikconfig.HTTPConfiguration{
			Services:          make(map[string]*traefikconfig.Service),
			Routers:           make(map[string]*traefikconfig.Router),
			Middlewares:       make(map[string]*traefikconfig.Middleware),
			ServersTransports: make(map[string]*traefikconfig.ServersTransport),
		},
		TCP: &traefikconfig.TCPConfiguration{
			Services:    make(map[string]*traefikconfig.TCPService),
			Routers:     make(map[string]*traefikconfig.TCPRouter),
			Middlewares: make(map[string]*traefikconfig.TCPMiddleware),
		},
	}

	for _, config := range configs {
		if config == nil {
			continue
		}

		// Merge HTTP
		if config.HTTP != nil {
			for name, service := range config.HTTP.Services {
				merged.HTTP.Services[name] = service
			}
			for name, router := range config.HTTP.Routers {
				merged.HTTP.Routers[name] = router
			}
			for name, middleware := range config.HTTP.Middlewares {
				merged.HTTP.Middlewares[name] = middleware
			}
			for name, transport := range config.HTTP.ServersTransports {
				merged.HTTP.ServersTransports[name] = transport
			}
		}

		// Merge TCP
		if config.TCP != nil {
			for name, service := range config.TCP.Services {
				merged.TCP.Services[name] = service
			}
			for name, router := range config.TCP.Routers {
				merged.TCP.Routers[name] = router
			}
			for name, middleware := range config.TCP.Middlewares {
				merged.TCP.Middlewares[name] = middleware
			}
		}
	}

	return merged
}

// GetAggregatedConfiguration fetches configurations from all child controllers and merges them
func GetAggregatedConfiguration(ctx context.Context, children []ChildController, localConfig *traefikconfig.Configuration) (*traefikconfig.Configuration, error) {
	configs := make([]*traefikconfig.Configuration, 0, len(children)+1)

	// Add local configuration without additional prefix (already has CommonName prefix)
	if localConfig != nil {
		configs = append(configs, localConfig)
	}

	// Fetch and prefix child configurations
	for _, child := range children {
		childConfig, err := child.FetchConfiguration(ctx)
		if err != nil {
			log.Printf("@W Failed to fetch configuration from child %s: %v\n", child.Name, err)
			// Continue with other children instead of failing completely
			continue
		}

		prefixedConfig := prefixConfigurationNames(childConfig, child.Name)
		configs = append(configs, prefixedConfig)
	}

	if len(configs) == 0 {
		return nil, fmt.Errorf("no configurations available to merge")
	}

	return mergeConfigurations(configs...), nil
}
