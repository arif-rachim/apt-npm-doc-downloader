package config

import (
	"fmt"
	"sort"
	"strings"
)

// CatalogEntry is a well known third party repository that can be added by a
// short key instead of typing a full sources.list line.
//
// Every entry here was verified against the repository's own Release file:
// URI, suite, components and the fact that it publishes an index for
// noble/amd64. They are upstream vendor repositories rather than community
// PPAs wherever the vendor publishes one, because a third party apt source
// is a supply chain dependency: prefer the one the project itself signs.
type CatalogEntry struct {
	Key string
	// Category groups the listing: containers, network, observability,
	// database, ai-gpu, languages, devops, tools.
	Category string
	// What the repository is for, shown by "airgap repos".
	Description string
	// Typical packages, to make the listing searchable.
	Packages string
	Source   AptSource
	// Note carries a caveat worth reading before adding the repository.
	Note string
}

func flat(uri string) AptSource {
	return AptSource{URI: uri, Suites: []string{"/"}}
}

func suite(uri, s string, comps ...string) AptSource {
	return AptSource{URI: uri, Suites: []string{s}, Components: comps}
}

// catalog is ordered by how commonly these are needed on a developer or
// DevOps workstation; "airgap repos" prints them in this order.
var catalog = []CatalogEntry{
	{
		Key: "docker", Category: "containers", Description: "Docker CE, containerd, buildx, compose",
		Packages: "docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin",
		Source:   suite("https://download.docker.com/linux/ubuntu", UbuntuCodename, "stable"),
	},
	{
		Key: "nvidia-cuda", Category: "ai-gpu", Description: "NVIDIA CUDA toolkit and drivers (GPU/AI)",
		Packages: "cuda-toolkit-12-6 cuda-drivers nvidia-driver-*",
		Source:   flat("https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2404/x86_64"),
		Note:     "pin driver, CUDA and framework versions together; mixing them is the usual cause of a broken GPU box",
	},
	{
		Key: "nvidia-container", Category: "ai-gpu", Description: "NVIDIA Container Toolkit (GPU inside Docker)",
		Packages: "nvidia-container-toolkit nvidia-container-runtime",
		Source:   flat("https://nvidia.github.io/libnvidia-container/stable/deb/amd64"),
	},
	{
		Key: "pgdg", Category: "database", Description: "PostgreSQL upstream (newer majors than Ubuntu ships)",
		Packages: "postgresql-18 postgresql-client-18 postgresql-18-pgvector libpq-dev",
		Source:   suite("https://apt.postgresql.org/pub/repos/apt", UbuntuCodename+"-pgdg", "main"),
	},
	{
		Key: "kubernetes", Category: "containers", Description: "Kubernetes tooling (pinned to v1.34)",
		Packages: "kubectl kubeadm kubelet",
		Source:   flat("https://pkgs.k8s.io/core:/stable:/v1.34/deb"),
		Note:     "the URL encodes the minor version; edit airgap.json to move to another v1.x line",
	},
	{
		Key: "hashicorp", Category: "devops", Description: "HashiCorp tooling",
		Packages: "terraform vault consul nomad packer boundary",
		Source:   suite("https://apt.releases.hashicorp.com", UbuntuCodename, "main"),
	},
	{
		Key: "github-cli", Category: "tools", Description: "GitHub CLI",
		Packages: "gh",
		Source:   suite("https://cli.github.com/packages", "stable", "main"),
	},
	{
		Key: "nodesource", Category: "languages", Description: "Node.js 22.x from NodeSource",
		Packages: "nodejs",
		Source:   suite("https://deb.nodesource.com/node_22.x", "nodistro", "main"),
		Note:     "change node_22.x in the URL for another Node major",
	},
	{
		Key: "microsoft", Category: "tools", Description: "Microsoft production repo for Ubuntu 24.04",
		Packages: "dotnet-sdk-8.0 powershell msodbcsql18",
		Source:   suite("https://packages.microsoft.com/ubuntu/24.04/prod", UbuntuCodename, "main"),
	},
	{
		Key: "vscode", Category: "tools", Description: "Visual Studio Code",
		Packages: "code",
		Source:   suite("https://packages.microsoft.com/repos/code", "stable", "main"),
	},
	{
		Key: "azure-cli", Category: "devops", Description: "Azure CLI",
		Packages: "azure-cli",
		Source:   suite("https://packages.microsoft.com/repos/azure-cli", UbuntuCodename, "main"),
	},
	{
		Key: "gcloud", Category: "devops", Description: "Google Cloud CLI",
		Packages: "google-cloud-cli kubectl",
		Source:   suite("https://packages.cloud.google.com/apt", "cloud-sdk", "main"),
	},
	{
		Key: "grafana", Category: "observability", Description: "Grafana, Loki, Tempo, Alloy (observability)",
		Packages: "grafana loki tempo alloy",
		Source:   suite("https://apt.grafana.com", "stable", "main"),
	},
	{
		Key: "influxdata", Category: "observability", Description: "InfluxDB and Telegraf (metrics)",
		Packages: "influxdb2 telegraf",
		Source:   suite("https://repos.influxdata.com/debian", "stable", "main"),
	},
	{
		Key: "elastic", Category: "observability", Description: "Elasticsearch, Kibana, Beats (8.x)",
		Packages: "elasticsearch kibana filebeat metricbeat",
		Source:   suite("https://artifacts.elastic.co/packages/8.x/apt", "stable", "main"),
	},
	{
		Key: "mongodb", Category: "database", Description: "MongoDB Community 8.0",
		Packages: "mongodb-org mongodb-mongosh",
		Source:   suite("https://repo.mongodb.org/apt/ubuntu", UbuntuCodename+"/mongodb-org/8.0", "multiverse"),
	},
	{
		Key: "helm", Category: "containers", Description: "Helm",
		Packages: "helm",
		Source:   suite("https://packages.buildkite.com/helm-linux/helm-debian/any", "any", "main"),
	},
	{
		Key: "tailscale", Category: "network", Description: "Tailscale VPN",
		Packages: "tailscale",
		Source:   suite("https://pkgs.tailscale.com/stable/ubuntu", UbuntuCodename, "main"),
	},
	{
		Key: "winehq", Category: "tools", Description: "WineHQ",
		Packages: "winehq-stable",
		Source:   suite("https://dl.winehq.org/wine-builds/ubuntu", UbuntuCodename, "main"),
	},
	{
		Key: "deadsnakes", Category: "languages", Description: "Multiple Python versions (community PPA)",
		Packages: "python3.11 python3.13 python3.13-venv",
		Source:   PPASource("deadsnakes", "ppa"),
		Note:     "community PPA, not upstream; for project dependencies prefer uv over apt",
	},
	{
		Key: "ondrej-php", Category: "languages", Description: "Multiple PHP versions (community PPA)",
		Packages: "php8.3 php8.4-fpm",
		Source:   PPASource("ondrej", "php"),
		Note:     "community PPA, not upstream",
	},
	{
		Key: "ansible", Category: "devops", Description: "Ansible (community PPA)",
		Packages: "ansible",
		Source:   PPASource("ansible", "ansible"),
		Note:     "community PPA, not upstream",
	},
	{
		Key: "nginx", Category: "network", Description: "NGINX stable from nginx.org",
		Packages: "nginx nginx-module-njs",
		Source:   suite("https://nginx.org/packages/ubuntu", UbuntuCodename, "nginx"),
		Note:     "the component is \"nginx\", not \"main\"; use nginx-mainline for HTTP/3 and newer modules",
	},
	{
		Key: "nginx-mainline", Category: "network", Description: "NGINX mainline from nginx.org",
		Packages: "nginx",
		Source:   suite("https://nginx.org/packages/mainline/ubuntu", UbuntuCodename, "nginx"),
	},
	{
		Key: "cloudflared", Category: "network", Description: "Cloudflare Tunnel client",
		Packages: "cloudflared",
		Source:   suite("https://pkg.cloudflare.com/cloudflared", UbuntuCodename, "main"),
	},
	{
		Key: "caddy", Category: "network", Description: "Caddy web server with automatic HTTPS",
		Packages: "caddy",
		Source:   suite("https://dl.cloudsmith.io/public/caddy/stable/deb/debian", "any-version", "main"),
	},
	{
		Key: "redis", Category: "database", Description: "Redis upstream",
		Packages: "redis-server redis-tools",
		Source:   suite("https://packages.redis.io/deb", UbuntuCodename, "main"),
	},
	{
		Key: "rabbitmq", Category: "database", Description: "RabbitMQ message broker",
		Packages: "rabbitmq-server",
		Source:   suite("https://packagecloud.io/rabbitmq/rabbitmq-server/ubuntu", UbuntuCodename, "main"),
		Note:     "rabbitmq-server also needs a matching Erlang; add the rabbitmq/erlang repo too if Ubuntu's erlang is too old",
	},
	{
		Key: "gitlab-runner", Category: "devops", Description: "GitLab CI runner",
		Packages: "gitlab-runner",
		Source:   suite("https://packages.gitlab.com/runner/gitlab-runner/ubuntu", UbuntuCodename, "main"),
	},
	{
		Key: "1password-cli", Category: "tools", Description: "1Password CLI for secret injection",
		Packages: "1password-cli",
		Source:   suite("https://downloads.1password.com/linux/debian/amd64", "stable", "main"),
	},
	{
		Key: "git-core", Category: "tools", Description: "Latest git (community PPA)",
		Packages: "git git-lfs",
		Source:   PPASource("git-core", "ppa"),
		Note:     "community PPA, not upstream",
	},
	{
		Key: "firefox-deb", Category: "tools", Description: "Firefox as a .deb instead of a snap (community PPA)",
		Packages: "firefox",
		Source:   PPASource("mozillateam", "ppa"),
		Note:     "community PPA; needs apt pinning against the Ubuntu snap transition package",
	},
}

// Catalog returns the known repositories in presentation order.
func Catalog() []CatalogEntry { return catalog }

// LookupCatalog resolves a short key such as "docker" or "pgdg".
func LookupCatalog(key string) (CatalogEntry, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, e := range catalog {
		if e.Key == key {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

// CatalogKeys lists every key, for error messages.
func CatalogKeys() string {
	keys := make([]string, 0, len(catalog))
	for _, e := range catalog {
		keys = append(keys, e.Key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// SourcesFromCatalog resolves a list of keys into sources.
func SourcesFromCatalog(keys []string) ([]AptSource, error) {
	var out []AptSource
	for _, k := range keys {
		e, ok := LookupCatalog(k)
		if !ok {
			return nil, fmt.Errorf("unknown repository %q; run \"airgap repos\" to see the list (%s)", k, CatalogKeys())
		}
		src := e.Source
		if src.Name == "" {
			src.Name = e.Key
		}
		out = append(out, src)
	}
	return out, nil
}
