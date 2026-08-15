package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opentalon/tln-language/internal/diagnostic"
	"github.com/opentalon/tln-language/internal/mod"
)

// bundleTlnVersion pins the tln-language version the generated bundle compiles
// against. v0.11.0 is the first release with the connector runtime
// (WithPlugin/WithEnv) the bundle relies on.
const bundleTlnVersion = "v0.11.0"

const bundleDir = ".tln/bundle"

// runInit scaffolds a new project: mod.tln + rules.tln + .gitignore.
func runInit() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: tln init <project-name>")
		os.Exit(diagnostic.ExitUsage)
	}
	name := os.Args[2]
	if err := os.MkdirAll(name, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "tln init: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
	files := map[string]string{
		"mod.tln":    initModTln,
		"rules.tln":  initRulesTln,
		".gitignore": ".tln/\n",
	}
	for base, content := range files {
		path := filepath.Join(name, base)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "tln init: %v\n", err)
			os.Exit(diagnostic.ExitError)
		}
	}
	fmt.Printf("created %s/ (mod.tln, rules.tln)\n  cd %s && tln bundle && tln run rules.tln\n", name, name)
}

const initModTln = `# Plugins this project uses, Bundler-style. ` + "`tln bundle`" + ` compiles them in.
plugin "io" "v0.1.0"
`

const initRulesTln = `workflow "hello" {
  step "greet" {
    tool "io" "writeln" { text "hello from tln" }
  }
}
`

// runBundle reads mod.tln, generates a project-local bootstrap module, and
// compiles it into a project-local ` + "`tln`" + ` binary that has the declared
// plugins baked in (the Bundler ` + "`bundle install`" + ` analog).
func runBundle() {
	src, err := os.ReadFile("mod.tln")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tln bundle: no mod.tln in the current directory (run `tln init <name>` first)")
		os.Exit(diagnostic.ExitError)
	}
	m, err := mod.Parse(string(src))
	if err != nil {
		fmt.Fprintf(os.Stderr, "tln bundle: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}

	goMod, mainGo, lock, err := generateBundle(m.Plugins)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tln bundle: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "tln bundle: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
	write := func(base, content string) {
		if err := os.WriteFile(filepath.Join(bundleDir, base), []byte(content), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "tln bundle: %v\n", err)
			os.Exit(diagnostic.ExitError)
		}
	}
	write("go.mod", goMod)
	write("main.go", mainGo)

	fmt.Printf("bundling %d plugin(s)…\n", len(m.Plugins))
	for _, step := range [][]string{
		{"go", "mod", "tidy"},
		{"go", "build", "-o", "tln", "."},
	} {
		cmd := exec.Command(step[0], step[1:]...)
		cmd.Dir = bundleDir
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "tln bundle: %s failed: %v\n", strings.Join(step, " "), err)
			os.Exit(diagnostic.ExitError)
		}
	}
	if err := os.WriteFile("mod.lock", []byte(lock), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "tln bundle: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
	fmt.Printf("bundled → %s/tln  (run: tln run <file.tln>)\n", bundleDir)
}

// generateBundle produces the bootstrap module's go.mod, main.go, and mod.lock
// for the given plugins. Pure (no filesystem), so it is unit-tested directly.
// Tool plugins are registered via WithPlugin; a store plugin (at most one) is
// wired via WithFactStore after loading config/store.tln (Active Record: the
// store is chosen by the manifest, defaulting to memory when none is declared).
// Errors if more than one store plugin is declared.
func generateBundle(plugins []mod.Plugin) (goMod, mainGo, lock string, err error) {
	var req, imports, regs, locks strings.Builder
	req.WriteString("\tgithub.com/opentalon/tln-language " + bundleTlnVersion + "\n")
	storeAlias, storeCount := "", 0
	for i, p := range plugins {
		req.WriteString(fmt.Sprintf("\t%s %s\n", p.Module, p.Version))
		alias := fmt.Sprintf("p%d", i)
		imports.WriteString(fmt.Sprintf("\t%s %q\n", alias, p.Module))
		locks.WriteString(fmt.Sprintf("%s %s %s\n", p.Name, p.Module, p.Version))
		if p.Store {
			storeCount++
			storeAlias = alias
		} else {
			regs.WriteString(fmt.Sprintf("\t\ttln.WithPlugin(%q, %s.Factory),\n", p.Name, alias))
		}
	}
	if storeCount > 1 {
		return "", "", "", fmt.Errorf("mod.tln declares %d store plugins; at most one is allowed", storeCount)
	}

	// A store plugin is wired after the opts literal: load config/store.tln,
	// build the FactStore via the plugin's Factory, and override the default
	// in-memory store. No store plugin → the memory default stands.
	storeWiring := ""
	if storeCount == 1 {
		storeWiring = fmt.Sprintf(`	if spec, ok, serr := tln.LoadStoreConfig("config/store.tln"); serr != nil {
		fmt.Fprintln(os.Stderr, serr)
		os.Exit(1)
	} else if ok {
		store, serr := %s.Factory(spec)
		if serr != nil {
			fmt.Fprintln(os.Stderr, serr)
			os.Exit(1)
		}
		opts = append(opts, tln.WithFactStore(store))
	}
`, storeAlias)
	}

	goMod = "module tlnbundle\n\ngo 1.25.0\n\nrequire (\n" + req.String() + ")\n"

	mainGo = `// Code generated by "tln bundle" from mod.tln. DO NOT EDIT.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opentalon/tln-language/pkg/tln"
` + imports.String() + `)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tlnbundle <file.tln>")
		os.Exit(2)
	}
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	opts := []tln.Option{
		tln.WithFactStore(tln.NewMemoryStore()),
		tln.WithEnv(os.LookupEnv),
` + regs.String() + `	}
` + storeWiring + `	res, err := tln.Run(context.Background(), string(src), opts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("%+v\n", res)
}
`
	lock = "# generated by `tln bundle` — do not edit\n" + locks.String()
	return goMod, mainGo, lock, nil
}

// maybeExecBundle re-execs the project-local bundled binary for `tln run` when
// one exists, so a program using plugins runs with them loaded. Returns true if
// it handled the run (the process has exited).
func maybeExecBundle(file string, extraArgs []string) bool {
	bin := filepath.Join(bundleDir, "tln")
	if _, err := os.Stat(bin); err != nil {
		return false
	}
	args := append([]string{file}, extraArgs...)
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "tln run: bundle: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
	os.Exit(0)
	return true
}
