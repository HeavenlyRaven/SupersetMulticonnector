package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/acme/superset-federation/internal/jsonio"
)

// maxSQLiteUploadBytes caps a single browser upload — generous for a
// demo/sample dataset, small enough that a mistaken multi-GB upload can't
// fill the user_files volume from the UI.
const maxSQLiteUploadBytes = 200 << 20 // 200 MiB

// sqliteFileInfo is one row of `fedctl source sqlite-files list` — every
// regular file already sitting in the user_files volume, regardless of how
// it got there (`fedctl seed sqlite` or a browser upload), so both paths
// feed the same picker.
type sqliteFileInfo struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
}

// sqlitePutResult is `fedctl source sqlite-files put`'s response — the same
// shape as sqliteFileInfo plus whether this call replaced an existing file
// (so the UI can say "Replaced" vs "Uploaded").
type sqlitePutResult struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	Replaced  bool   `json:"replaced"`
}

func newSQLiteFilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sqlite-files",
		Short: "List or upload SQLite files available to attach as a source",
	}
	cmd.AddCommand(newSQLiteFilesListCmd())
	cmd.AddCommand(newSQLiteFilesPutCmd())
	return cmd
}

func newSQLiteFilesListCmd() *cobra.Command {
	var jsonMode bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List regular files sitting in the user_files volume",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := loadApp(jsonMode)
			if err != nil {
				return err
			}
			result, err := listSQLiteFiles(app.UserFilesDir)
			return finish(jsonMode, result, err)
		},
	}
	cmd.Flags().BoolVar(&jsonMode, "json", false, "write a jsonio envelope to stdout")
	return cmd
}

func listSQLiteFiles(dir string) ([]sqliteFileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, jsonio.NewError(jsonio.CodeInternal, "cannot list user_files directory: "+err.Error(), "")
	}
	out := []sqliteFileInfo{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, sqliteFileInfo{Name: e.Name(), SizeBytes: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func newSQLiteFilesPutCmd() *cobra.Command {
	var jsonMode bool
	var name string
	var replace bool
	cmd := &cobra.Command{
		Use:   "put",
		Short: "Write stdin's bytes into the user_files volume under --name",
		Long: "Writes stdin's bytes into the user_files volume under --name. This is the\n" +
			"Python shim's browser-upload path — unlike `fedctl seed sqlite`, which copies\n" +
			"a HOST file via a throwaway docker container, this runs inside the superset\n" +
			"container against bytes already received over HTTP, so it needs no docker\n" +
			"socket and no host filesystem access.\n\n" +
			"Refuses to overwrite an existing file unless --replace is given — a bare\n" +
			"`put` is for a genuinely new file; --replace is the explicit \"refresh this\n" +
			"file's contents\" action, since SQLite has no live network connection to\n" +
			"re-read (unlike postgres/mysql sources) and a fresh snapshot is the only\n" +
			"way to pick up upstream changes.",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := loadApp(jsonMode)
			if err != nil {
				return err
			}
			result, err := putSQLiteFile(app.UserFilesDir, name, replace, cmd.InOrStdin())
			return finish(jsonMode, result, err)
		},
	}
	cmd.Flags().BoolVar(&jsonMode, "json", false, "write a jsonio envelope to stdout")
	cmd.Flags().StringVar(&name, "name", "", "destination filename inside the user_files volume")
	cmd.Flags().BoolVar(&replace, "replace", false, "allow overwriting an existing file with this name")
	return cmd
}

// putSQLiteFile validates name with the same seedNameRe used by `fedctl
// seed sqlite` (see seed.go) — one filename policy regardless of which path
// got the bytes there — then streams r to a temp file in dir and renames it
// into place, so a client that disconnects mid-upload or a stream that
// exceeds maxSQLiteUploadBytes never leaves a partial file at the final
// name, and so a source currently reading dest never sees a half-written
// file: os.Rename is atomic on POSIX, replacing dest in a single syscall
// rather than truncating-then-rewriting it in place.
//
// Refuses to overwrite an existing file unless allowOverwrite is true —
// replacing bytes under a name ClickHouse might already have an attached
// source against is deliberate (the "Replace" button), not something a
// same-named re-upload should do by accident.
func putSQLiteFile(dir, name string, allowOverwrite bool, r io.Reader) (any, error) {
	if name == "" || name == "." || name == ".." || !seedNameRe.MatchString(name) {
		return nil, jsonio.NewError(jsonio.CodeInvalidPath,
			fmt.Sprintf("invalid destination name %q: must match %s", name, seedNameRe.String()), "")
	}
	dest := filepath.Join(dir, name)
	existed := false
	if _, err := os.Stat(dest); err == nil {
		existed = true
		if !allowOverwrite {
			return nil, jsonio.NewError(jsonio.CodeFileExists,
				fmt.Sprintf("a file named %q already exists", name),
				"pass --replace to overwrite it, or choose a different name")
		}
	} else if !os.IsNotExist(err) {
		return nil, jsonio.NewError(jsonio.CodeInternal, "checking destination: "+err.Error(), "")
	}

	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return nil, jsonio.NewError(jsonio.CodeInternal, "creating temp file: "+err.Error(), "")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	n, copyErr := io.Copy(tmp, io.LimitReader(r, maxSQLiteUploadBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return nil, jsonio.NewError(jsonio.CodeInternal, "writing upload: "+copyErr.Error(), "")
	}
	if closeErr != nil {
		return nil, jsonio.NewError(jsonio.CodeInternal, "writing upload: "+closeErr.Error(), "")
	}
	if n > maxSQLiteUploadBytes {
		return nil, jsonio.NewError(jsonio.CodeInvalidRequest,
			fmt.Sprintf("upload exceeds the %d MiB limit", maxSQLiteUploadBytes/(1<<20)), "")
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return nil, jsonio.NewError(jsonio.CodeInternal, "setting file permissions: "+err.Error(), "")
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return nil, jsonio.NewError(jsonio.CodeInternal, "moving upload into place: "+err.Error(), "")
	}
	return sqlitePutResult{Name: name, SizeBytes: n, Replaced: existed}, nil
}
