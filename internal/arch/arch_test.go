// Package arch contains no code. It contains the architecture, expressed as a
// test that fails.
//
// The load-bearing claim of this project is that the matching engine knows
// nothing about transport and that the chaos layer cannot reach it. Every other
// way of defending that claim — a paragraph in the README, a header comment, a
// careful diagram — is documentation, and documentation decays the first time
// somebody is in a hurry. This file is the only defence that survives the next
// edit, and it is the answer to the question that actually matters: not
// "is it decoupled?" but "how do you know it stays decoupled?"
package arch

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const module = "github.com/ziaulalam1/open-outcry"

type rule struct {
	// modulePackages this package may import, transitively. Empty means it must
	// not import ANY other package in this module.
	mayImport []string
	// directBans are import paths this package must not name directly. Checked
	// against direct imports rather than the transitive set, because innocuous
	// stdlib packages drag in half the world (fmt imports os) and a transitive
	// ban would be either vacuous or unsatisfiable.
	directBans []string
	// hardBans must not appear anywhere in the transitive closure. Reserved for
	// things no legitimate dependency can pull in.
	hardBans []string
	why      string
}

// Every package in internal/ must appear here. A package with no rule fails the
// enumeration test below, which is deliberate: adding a package to this project
// requires stating what it is allowed to depend on.
var rules = map[string]rule{
	"internal/invariant": {
		mayImport: nil,
		hardBans:  []string{"net", "net/http", "encoding/json", module},
		why: "The checker must not be able to name a single type in the engine. " +
			"If it could, it could read an aggregate the matcher computed, and " +
			"conservation would become a tautology that stays green through the " +
			"exact bug it exists to catch.",
	},
	"internal/engine": {
		mayImport:  []string{"internal/invariant"},
		directBans: []string{"time", "os", "encoding/json", "net", "net/http", "math/rand"},
		hardBans:   []string{"net/http", "github.com/gorilla/websocket"},
		why: "The engine has never heard of JSON, HTTP or WebSocket, and has no " +
			"clock: Seq is its only notion of time, which is what makes replay " +
			"exact and fuzzing deterministic.",
	},
	"internal/seed": {
		mayImport: []string{"internal/engine", "internal/invariant"},
		hardBans:  []string{"net/http", "encoding/json"},
		why:       "The opening ladder is just commands. It goes through the front door.",
	},
	"internal/wire": {
		mayImport: []string{"internal/engine", "internal/invariant"},
		hardBans:  []string{"net/http", "github.com/gorilla/websocket"},
		why: "wire is the airlock: the only package that may name the wire format. " +
			"It knows about domain values and about bytes, and nothing about sockets.",
	},
	"internal/loop": {
		mayImport:  []string{"internal/engine", "internal/invariant"},
		directBans: []string{"encoding/json", "net", "net/http"},
		hardBans:   []string{"net/http", "github.com/gorilla/websocket"},
		why: "The loop owns the book pointer. It publishes bytes through interfaces " +
			"declared consumer-side, so it can neither encode nor reach a socket — " +
			"which is what keeps encoding/json confined to one package.",
	},
	"internal/hub": {
		mayImport:  nil,
		directBans: []string{"encoding/json"},
		why: "The hub cannot name a domain type. That is what makes \"the transport " +
			"cannot reach the engine\" a compile-time fact. It owns the counters but " +
			"cannot marshal them, which is why StatsEncoder is injected.",
	},
	"internal/chaos": {
		mayImport:  nil,
		directBans: []string{"encoding/json", "net", "net/http"},
		hardBans:   []string{"github.com/gorilla/websocket", "net/http"},
		why: "Chaos is a decorator whose only outward reference is a func value. It " +
			"cannot name engine.Book or engine.Command, so \"the chaos layer cannot " +
			"reach the engine\" is enforced by the compiler and act two's claim holds.",
	},
	"internal/arch": {
		mayImport: nil,
		why:       "This package is the rules themselves.",
	},
}

// touchSources reads every Go source file in the module, so that Go's test
// cache treats them as inputs to this test.
//
// Without this, the architecture test is a LIE, and it is the most dangerous
// possible kind: it reaches its conclusions by shelling out to `go list`, so
// cmd/go's testlog records no dependency on any of the files it audits. A
// cached PASS is then replayed unchanged after somebody introduces a violation,
// and the guard whose entire purpose is to catch future edits reports `ok
// (cached)` forever. Measured, not theorised: planting `import "encoding/json"`
// in internal/engine and running `go test ./internal/arch/...` printed
// `ok (cached)`; the same run with -count=1 printed four failures.
//
// The fix is to make the cache key correct rather than to defeat caching. If
// any source file changes, this test re-runs.
func touchSources(t *testing.T) {
	t.Helper()
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "web":
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".go" || d.Name() == "go.mod" {
			if _, err := os.ReadFile(path); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading module sources: %v", err)
	}
}

func TestEveryInternalPackageHasARule(t *testing.T) {
	touchSources(t)
	for _, pkg := range listPackages(t, module+"/internal/...") {
		short := strings.TrimPrefix(pkg, module+"/")
		if _, ok := rules[short]; !ok {
			t.Errorf("package %s has no architecture rule.\n"+
				"Add one to internal/arch/arch_test.go stating what it may import. "+
				"A new package that quietly inherits no constraints is how the "+
				"engine boundary gets lost.", short)
		}
	}
}

func TestImportBoundaries(t *testing.T) {
	touchSources(t)
	for short, r := range rules {
		pkg := module + "/" + short
		if !packageExists(pkg) {
			continue // not built yet; the enumeration test guards the reverse case
		}
		t.Run(short, func(t *testing.T) {
			allowed := map[string]bool{}
			for _, a := range r.mayImport {
				allowed[module+"/"+a] = true
			}

			for _, dep := range deps(t, pkg) {
				if dep == pkg || !strings.HasPrefix(dep, module+"/") {
					continue
				}
				if !allowed[dep] {
					t.Errorf("%s imports %s (transitively), which is not allowed.\n  why: %s",
						short, strings.TrimPrefix(dep, module+"/"), r.why)
				}
			}

			direct := directImports(t, pkg)
			for _, ban := range r.directBans {
				for _, d := range direct {
					if d == ban {
						t.Errorf("%s directly imports %q, which is banned.\n  why: %s", short, ban, r.why)
					}
				}
			}

			all := deps(t, pkg)
			for _, ban := range r.hardBans {
				for _, d := range all {
					if d == ban {
						t.Errorf("%s reaches %q transitively, which is banned.\n  why: %s", short, ban, r.why)
					}
				}
			}
		})
	}
}

// The engine's dependency list is short enough to assert exactly. Pinning it
// makes any new dependency a conscious decision rather than an accident.
func TestEngineDependsOnAlmostNothing(t *testing.T) {
	touchSources(t)
	got := directImports(t, module+"/internal/engine")
	sort.Strings(got)
	want := []string{module + "/internal/invariant"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("engine direct imports = %v, want %v.\n"+
			"The engine currently imports NO standard library package at all. "+
			"That is worth keeping: it is the shortest possible answer to "+
			"\"what does your matching engine depend on?\"", got, want)
	}
}

// encoding/json is the airlock. Exactly one package is allowed to know the wire
// format exists; everything else trades in domain values.
func TestJSONIsConfinedToTheWirePackage(t *testing.T) {
	touchSources(t)
	allowed := map[string]bool{
		module + "/internal/wire": true,
		module + "/cmd/swarm":     true, // the load client speaks the wire format on purpose
	}
	for _, pkg := range listPackages(t, module+"/...") {
		if allowed[pkg] {
			continue
		}
		for _, d := range directImports(t, pkg) {
			if d == "encoding/json" {
				t.Errorf("%s imports encoding/json. Only internal/wire may. "+
					"Serialisation living in one place is what keeps every other "+
					"package trading in domain values.", strings.TrimPrefix(pkg, module+"/"))
			}
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func run(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func listPackages(t *testing.T, pattern string) []string {
	t.Helper()
	return run(t, "list", pattern)
}

func deps(t *testing.T, pkg string) []string {
	t.Helper()
	return run(t, "list", "-deps", pkg)
}

func directImports(t *testing.T, pkg string) []string {
	t.Helper()
	return run(t, "list", "-f", `{{join .Imports "\n"}}`, pkg)
}

func packageExists(pkg string) bool {
	return exec.Command("go", "list", pkg).Run() == nil
}
