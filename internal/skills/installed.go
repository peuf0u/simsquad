package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Installed describes the skills found in an app repo, read back from the
// generated-file headers.
type Installed struct {
	// Dir is the absolute skills directory that was read.
	Dir string
	// Version is the simsquad version that wrote the skills.
	Version string
	// Contract is the skill contract the skills were written for. When the
	// skills disagree (a partial upgrade), it is the highest one found, so
	// a caller comparing it with Contract errs towards refusing.
	Contract int
	// Missing lists the generated files ("<skill>/<path>") of every skill in
	// Names that are absent or have no header.
	Missing []string
	// Edited is true when any generated skill file was changed by hand
	// since install.
	Edited bool
}

// ReadInstalled reads the installed skills' headers from dir (empty means
// root/DefaultDir; a relative dir resolves against root). It returns nil,
// nil when no skill is installed there.
func ReadInstalled(root, dir string) (*Installed, error) {
	in, err := newInstaller(Options{Root: root, Dir: dir})
	if err != nil {
		return nil, err
	}
	res := &Installed{Dir: in.dir}
	found := false
	for _, name := range Names {
		files, err := skillFiles(name)
		if err != nil {
			return nil, fmt.Errorf("skills: list %s: %w", name, err)
		}
		for _, f := range files {
			rel := name + "/" + f
			b, err := os.ReadFile(filepath.Join(in.dir, name, filepath.FromSlash(f)))
			if errors.Is(err, os.ErrNotExist) {
				res.Missing = append(res.Missing, rel)
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("skills: read %s: %w", rel, err)
			}
			h, body, ok := parse(string(b))
			if !ok {
				res.Missing = append(res.Missing, rel)
				res.Edited = true
				continue
			}
			found = true
			if checksum(body) != h.Sum {
				res.Edited = true
			}
			if h.Contract >= res.Contract {
				res.Contract, res.Version = h.Contract, h.Version
			}
		}
	}
	if !found {
		return nil, nil
	}
	return res, nil
}
