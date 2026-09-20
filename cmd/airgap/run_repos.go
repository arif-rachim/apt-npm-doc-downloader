package main

import (
	"fmt"
	"strings"

	"airgapkit/internal/config"
)

var categoryOrder = []struct{ key, title string }{
	{"containers", "Containers and orchestration"},
	{"ai-gpu", "AI and GPU"},
	{"database", "Databases and brokers"},
	{"network", "Reverse proxy and networking"},
	{"observability", "Observability"},
	{"devops", "DevOps and cloud"},
	{"languages", "Language runtimes"},
	{"tools", "Developer tools"},
}

// printRepoCatalog lists the repositories that can be added with -repo.
func printRepoCatalog() {
	fmt.Println("Built-in apt repositories for Ubuntu 24.04 / amd64.")
	fmt.Println("Every URI, suite and component below was checked against the repository's")
	fmt.Println("own Release file. The Ubuntu archives are always included and need no key.")
	fmt.Println()
	fmt.Println("  airgap fetch apt -repo docker,pgdg -pkg docker-ce,postgresql-18")
	fmt.Println()

	byCat := map[string][]config.CatalogEntry{}
	for _, e := range config.Catalog() {
		byCat[e.Category] = append(byCat[e.Category], e)
	}
	for _, c := range categoryOrder {
		entries := byCat[c.key]
		if len(entries) == 0 {
			continue
		}
		fmt.Printf("%s\n", c.title)
		for _, e := range entries {
			fmt.Printf("  %-16s %s\n", e.Key, e.Description)
			fmt.Printf("  %-16s %s\n", "", e.Packages)
			fmt.Printf("  %-16s %s\n", "", sourceLine(e.Source))
			if e.Note != "" {
				fmt.Printf("  %-16s note: %s\n", "", e.Note)
			}
		}
		fmt.Println()
	}
	fmt.Println("A third-party repository is a supply-chain dependency, so add only the ones")
	fmt.Println("you actually use. These are upstream vendor repositories except where the")
	fmt.Println("note says \"community PPA\". To verify a repository's signature as well as")
	fmt.Println("its checksums, set \"keyring\" on that source in airgap.json (needs gpgv).")
	fmt.Println()
	fmt.Println("Not listed because they are not distributed through apt: Ollama and Open")
	fmt.Println("WebUI (install script or container), Prometheus and node_exporter (tarball")
	fmt.Println("or container), PyTorch and most of the Python AI stack (use pypi instead).")
}

func sourceLine(s config.AptSource) string {
	out := s.URI
	if len(s.Suites) > 0 {
		out += " " + strings.Join(s.Suites, " ")
	}
	if len(s.Components) > 0 {
		out += " " + strings.Join(s.Components, " ")
	}
	return out
}
