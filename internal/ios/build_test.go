package ios

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/peuf0u/simsquad/internal/util"
)

func TestParseBuildSettings(t *testing.T) {
	// Shape of `xcodebuild -showBuildSettings -json`, modelled on the
	// MyApp scheme whose product is "MyApp Beta.app" (scheme != product).
	const j = `[
	  {
	    "target": "MyApp",
	    "action": "build",
	    "buildSettings": {
	      "FULL_PRODUCT_NAME": "MyApp Beta.app",
	      "WRAPPER_NAME": "MyApp Beta.app",
	      "PRODUCT_BUNDLE_IDENTIFIER": "com.example.myapp",
	      "TARGET_BUILD_DIR": "/Users/x/.cache/simsquad/derived/ios/Build/Products/Debug-iphonesimulator"
	    }
	  }
	]`
	bs, err := parseBuildSettings([]byte(j))
	if err != nil {
		t.Fatalf("parseBuildSettings: %v", err)
	}
	if bs.ProductName != "MyApp Beta.app" {
		t.Errorf("ProductName = %q", bs.ProductName)
	}
	if bs.BundleID != "com.example.myapp" {
		t.Errorf("BundleID = %q", bs.BundleID)
	}
	if filepath.Base(bs.BuildDir) != "Debug-iphonesimulator" {
		t.Errorf("BuildDir = %q", bs.BuildDir)
	}
}

func TestParseBuildSettingsWrapperFallback(t *testing.T) {
	// No FULL_PRODUCT_NAME — must fall back to WRAPPER_NAME.
	const j = `[{"buildSettings":{"WRAPPER_NAME":"App.app","PRODUCT_BUNDLE_IDENTIFIER":"com.x.app"}}]`
	bs, err := parseBuildSettings([]byte(j))
	if err != nil {
		t.Fatalf("parseBuildSettings: %v", err)
	}
	if bs.ProductName != "App.app" {
		t.Errorf("ProductName = %q, want App.app", bs.ProductName)
	}
}

func TestParseBuildSettingsEmpty(t *testing.T) {
	if _, err := parseBuildSettings([]byte(`[]`)); err == nil {
		t.Error("expected error on empty settings array")
	}
}

func TestResolveProjectPath(t *testing.T) {
	mkproj := func(dir, name string) {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("scheme-named fast path", func(t *testing.T) {
		repo := t.TempDir()
		mkproj(repo, "MyApp.xcodeproj")
		got, err := resolveProjectPath(repo, "MyApp")
		if err != nil || filepath.Base(got) != "MyApp.xcodeproj" {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("project name differs from scheme", func(t *testing.T) {
		// Genuinely different name (not just case) so the fast path misses on
		// any filesystem and the single-project glob path is exercised.
		repo := t.TempDir()
		mkproj(repo, "MyCompanyApp.xcodeproj") // scheme is "Production"
		got, err := resolveProjectPath(repo, "Production")
		if err != nil || filepath.Base(got) != "MyCompanyApp.xcodeproj" {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("multiple projects, scheme picks one", func(t *testing.T) {
		repo := t.TempDir()
		mkproj(repo, "Sample.xcodeproj")
		mkproj(repo, "MainApp.xcodeproj")
		got, err := resolveProjectPath(repo, "MainApp")
		if err != nil || filepath.Base(got) != "MainApp.xcodeproj" {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("multiple projects, no match errors", func(t *testing.T) {
		repo := t.TempDir()
		mkproj(repo, "A.xcodeproj")
		mkproj(repo, "B.xcodeproj")
		if _, err := resolveProjectPath(repo, "Nonexistent"); err == nil {
			t.Error("expected error when no project matches scheme")
		}
	})

	t.Run("no project errors", func(t *testing.T) {
		if _, err := resolveProjectPath(t.TempDir(), "X"); err == nil {
			t.Error("expected error when no .xcodeproj exists")
		}
	})
}

func TestResolvedFileArgs(t *testing.T) {
	t.Run("no resolved file", func(t *testing.T) {
		repo := t.TempDir()
		project := filepath.Join(repo, "App.xcodeproj")
		os.MkdirAll(project, 0o755)
		if args := resolvedFileArgs(repo, project); args != nil {
			t.Errorf("expected nil, got %v", args)
		}
	})

	t.Run("embedded resolved file", func(t *testing.T) {
		repo := t.TempDir()
		project := filepath.Join(repo, "App.xcodeproj")
		swiftpm := filepath.Join(project, "project.xcworkspace", "xcshareddata", "swiftpm")
		os.MkdirAll(swiftpm, 0o755)
		os.WriteFile(filepath.Join(swiftpm, "Package.resolved"), []byte("{}"), 0o644)
		args := resolvedFileArgs(repo, project)
		if len(args) != 1 || args[0] != "-onlyUsePackageVersionsFromResolvedFile" {
			t.Errorf("got %v", args)
		}
	})

	t.Run("repo-root resolved file", func(t *testing.T) {
		repo := t.TempDir()
		project := filepath.Join(repo, "App.xcodeproj")
		os.MkdirAll(project, 0o755)
		os.WriteFile(filepath.Join(repo, "Package.resolved"), []byte("{}"), 0o644)
		if args := resolvedFileArgs(repo, project); len(args) != 1 {
			t.Errorf("expected the pin flag, got %v", args)
		}
	})
}

// Product lookup must only ever look in the repo's own derived tree; another
// repo's leftover products must not be picked up (issue #15).
func TestAppFromSettingsUsesRepoDerivedDir(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	repoA, repoB := "/work/a/app", "/work/b/app"

	app := filepath.Join(util.IOSDerivedDataDir(repoA), simulatorProductsSubdir, "MyApp Beta.app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	bs := buildSettings{ProductName: "MyApp Beta.app"}

	got, err := appFromSettings(repoA, bs)
	if err != nil || got != app {
		t.Fatalf("appFromSettings(repoA) = %q, %v; want %q", got, err, app)
	}
	if got, err := appFromSettings(repoB, bs); err == nil {
		t.Fatalf("repoB picked up repoA's product %q", got)
	}
	want := filepath.Join(util.IOSDerivedDataDir(repoA), simulatorProductsSubdir, "MyApp.app")
	if got := ExpectedAppPath(repoA, "MyApp"); got != want {
		t.Errorf("ExpectedAppPath = %q, want %q", got, want)
	}
}
