package dirmove

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/crux/ccctl/internal/claudestate"
)

// Result reports what Execute did beyond the plan.
type Result struct {
	Config   []claudestate.KeyCopy
	History  int
	Warnings []string
}

// Execute applies a plan in this order:
//
//  1. Move the directory (skipped with SessionsOnly).
//  2. Rename the project folders, or merge them into existing ones.
//  3. Copy the ~/.claude.json entries.
//  4. Move the prompt history entries.
//
// When step 1 fails, nothing has changed. When step 2 fails, Execute
// undoes every rename and merge step and moves the directory back. A
// move across volumes is not copied back; the error then names the
// command that finishes the move. Failures in steps 3 and 4 are
// warnings: the move itself is complete.
func Execute(p *Plan) (*Result, error) {
	if err := p.BlockedError(); err != nil {
		return nil, err
	}

	var undo undoStack
	if !p.SessionsOnly {
		if err := moveDir(p); err != nil {
			return nil, err
		}
		if !p.CrossVolume {
			undo.push(func() error { return renameNew(p.Dst, p.Src) })
		}
	}

	for _, f := range p.Folders {
		from, to := filepath.Join(p.Root, f.Name), filepath.Join(p.Root, f.NewName)
		var err error
		if f.Merge {
			err = mergeFolder(from, to, &undo)
		} else if err = renameNew(from, to); err == nil {
			undo.push(func() error { return renameNew(to, from) })
		}
		if err != nil {
			msg := fmt.Sprintf("project folder %s: %v", f.Name, err)
			if rbErr := undo.run(); rbErr != nil {
				msg += "\nundo was incomplete:\n  " + rbErr.Error()
			} else if p.CrossVolume {
				msg += fmt.Sprintf("\nproject folders are unchanged; the directory is at %s.\nfinish with: ccctl move --sessions-only %q %q", p.Dst, p.Src, p.Dst)
			} else {
				msg += "\nall changes were undone"
			}
			return nil, errors.New(msg)
		}
	}

	res := &Result{}
	r := p.Rebase()
	var err error
	if res.Config, err = claudestate.CopyConfig(r); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("~/.claude.json not updated: %v", err))
	}
	if res.History, err = claudestate.MoveHistory(r); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("prompt history not moved: %v", err))
	}
	return res, nil
}

// undoStack collects the inverse of each step; run applies them in
// reverse order and keeps going past failures.
type undoStack []func() error

func (u *undoStack) push(f func() error) { *u = append(*u, f) }

func (u undoStack) run() error {
	var errs []error
	for i := len(u) - 1; i >= 0; i-- {
		if err := u[i](); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func moveDir(p *Plan) error {
	if _, err := os.Lstat(p.Dst); err == nil {
		return fmt.Errorf("%s appeared after the check; nothing changed", p.Dst)
	}
	if p.CrossVolume {
		cmd := exec.Command("/bin/mv", "--", p.Src, p.Dst)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("/bin/mv failed: %v; project folders are unchanged, check %s and %s", err, p.Src, p.Dst)
		}
		return nil
	}
	if err := os.Rename(p.Src, p.Dst); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("%s and %s are on different volumes; add --cross-volume", p.Src, p.Dst)
		}
		return err
	}
	return nil
}

// mergeFolder moves every entry of src into the existing folder dst,
// then removes src. The rules match mergeClashes: an empty directory
// on either side counts as absent, the target keeps its own .DS_Store,
// and any other name present in both stops the merge.
func mergeFolder(src, dst string, undo *undoStack) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		n := e.Name()
		sp, dp := filepath.Join(src, n), filepath.Join(dst, n)
		dfi, derr := os.Lstat(dp)
		switch {
		case derr != nil:
			if err := renameNew(sp, dp); err != nil {
				return err
			}
			undo.push(func() error { return renameNew(dp, sp) })
		case n == ".DS_Store":
			if err := os.Remove(sp); err != nil {
				return err
			}
		case e.IsDir() && isEmptyDir(sp):
			if err := removeEmptyDir(sp, undo); err != nil {
				return err
			}
		case e.IsDir() && dfi.IsDir() && isEmptyDir(dp):
			if err := removeEmptyDir(dp, undo); err != nil {
				return err
			}
			if err := renameNew(sp, dp); err != nil {
				return err
			}
			undo.push(func() error { return renameNew(dp, sp) })
		default:
			return fmt.Errorf("%s exists in both %s and %s", n, filepath.Base(src), filepath.Base(dst))
		}
	}
	return removeEmptyDir(src, undo)
}

// removeEmptyDir removes an empty directory and records how to create
// it again with the same mode.
func removeEmptyDir(path string, undo *undoStack) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	mode := fi.Mode().Perm()
	undo.push(func() error {
		if err := os.Mkdir(path, mode); err != nil {
			return err
		}
		return os.Chmod(path, mode)
	})
	return nil
}

// renameNew renames from to to and refuses when to exists. os.Rename
// alone would replace an empty directory at to.
func renameNew(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s exists", to)
	}
	return os.Rename(from, to)
}
