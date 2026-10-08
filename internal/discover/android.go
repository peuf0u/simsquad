package discover

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/peuf0u/simsquad/internal/android"
	"github.com/peuf0u/simsquad/internal/util"
)

// AndroidImage is one entry under <sdk>/system-images/. The identifier matches
// the format avdmanager expects: "system-images;android-34;google_apis;arm64-v8a".
type AndroidImage struct {
	Identifier string
	APILevel   string // "android-34"
	Tag        string // "google_apis"
	Arch       string // "arm64-v8a"
}

// fallbackAVDDevices is the curated list returned when avdmanager isn't
// installed. Keep these names valid for any recent avdmanager version.
var fallbackAVDDevices = []string{"pixel_7", "pixel_8", "pixel_9", "pixel_tablet"}

// ListInstalledAndroidImages walks <sdk>/system-images/* and returns every
// installed image. A "package.xml" or "system.img" presence is the install
// heuristic. Returns nil on missing SDK.
func ListInstalledAndroidImages() []AndroidImage {
	root, err := android.SDKRoot()
	if err != nil {
		return nil
	}
	base := filepath.Join(root, "system-images")
	if !isDir(base) {
		return nil
	}
	var out []AndroidImage
	apiDirs, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	sortDirEntries(apiDirs)
	for _, apiEntry := range apiDirs {
		if !apiEntry.IsDir() {
			continue
		}
		apiDir := filepath.Join(base, apiEntry.Name())
		tagDirs, err := os.ReadDir(apiDir)
		if err != nil {
			continue
		}
		sortDirEntries(tagDirs)
		for _, tagEntry := range tagDirs {
			if !tagEntry.IsDir() {
				continue
			}
			tagDir := filepath.Join(apiDir, tagEntry.Name())
			archDirs, err := os.ReadDir(tagDir)
			if err != nil {
				continue
			}
			sortDirEntries(archDirs)
			for _, archEntry := range archDirs {
				if !archEntry.IsDir() {
					continue
				}
				archDir := filepath.Join(tagDir, archEntry.Name())
				if !isFile(filepath.Join(archDir, "package.xml")) && !isFile(filepath.Join(archDir, "system.img")) {
					continue
				}
				out = append(out, AndroidImage{
					Identifier: "system-images;" + apiEntry.Name() + ";" + tagEntry.Name() + ";" + archEntry.Name(),
					APILevel:   apiEntry.Name(),
					Tag:        tagEntry.Name(),
					Arch:       archEntry.Name(),
				})
			}
		}
	}
	return out
}

// ListAVDDeviceProfiles returns a short list of common device profile names
// accepted by avdmanager. When cmdline-tools is missing it falls back to a
// curated set so the wizard always has something to offer.
func ListAVDDeviceProfiles() []string {
	avdmanager, err := android.AVDManager()
	if err != nil {
		return append([]string{}, fallbackAVDDevices...)
	}
	r, err := util.Run(avdmanager, []string{"list", "device", "-c"}, util.RunOpts{})
	if err != nil || r.Code != 0 {
		return append([]string{}, fallbackAVDDevices...)
	}
	var names []string
	for _, line := range strings.Split(r.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	var pixels []string
	for _, n := range names {
		if strings.HasPrefix(strings.ToLower(n), "pixel") {
			pixels = append(pixels, n)
		}
	}
	if len(pixels) > 0 {
		return pixels
	}
	if len(names) > 20 {
		return names[:20]
	}
	return names
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func sortDirEntries(e []os.DirEntry) {
	sort.Slice(e, func(i, j int) bool { return e[i].Name() < e[j].Name() })
}
