// Package images pulls OCI images into a content-addressed local cache and
// safely applies their layers to an OCI root filesystem.
package images

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

type Record struct {
	Reference    string    `json:"reference"`
	References   []string  `json:"references,omitempty"`
	Digest       string    `json:"digest"`
	ImageID      string    `json:"imageID"`
	OS           string    `json:"os"`
	Architecture string    `json:"architecture"`
	RootFS       string    `json:"rootfs"`
	Layers       []string  `json:"layers"`
	PulledAt     time.Time `json:"pulledAt"`
}

// DefaultStore returns the per-user image cache directory. The CLI passes
// absolute rootfs paths from this cache to the daemon.
func DefaultStore() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find user cache directory: %w", err)
	}
	return filepath.Join(cache, "brain", "images"), nil
}

func ParseReference(raw string) (name.Reference, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return nil, errors.New("image reference must be non-empty and contain no surrounding whitespace")
	}
	ref, err := name.ParseReference(raw, name.WeakValidation)
	if err != nil {
		return nil, fmt.Errorf("parse image reference %q: %w", raw, err)
	}
	return ref, nil
}

// Pull resolves a tag or digest for the host architecture, verifies registry
// layer digests while caching blobs, and applies layers into an isolated rootfs.
func Pull(ctx context.Context, store, rawReference string) (Record, error) {
	ref, err := ParseReference(rawReference)
	if err != nil {
		return Record{}, err
	}
	image, err := remote.Image(ref,
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithPlatform(v1.Platform{OS: "linux", Architecture: runtime.GOARCH}),
	)
	if err != nil {
		return Record{}, fmt.Errorf("resolve image %q for linux/%s: %w", rawReference, runtime.GOARCH, err)
	}
	digest, err := image.Digest()
	if err != nil {
		return Record{}, fmt.Errorf("read image manifest digest: %w", err)
	}
	config, err := image.ConfigFile()
	if err != nil {
		return Record{}, fmt.Errorf("read image config: %w", err)
	}
	if config.OS != "linux" || config.Architecture != runtime.GOARCH {
		return Record{}, fmt.Errorf("image platform is %s/%s, expected linux/%s", config.OS, config.Architecture, runtime.GOARCH)
	}

	key := strings.TrimPrefix(digest.String(), "sha256:")
	if len(key) != 64 {
		return Record{}, fmt.Errorf("unexpected manifest digest %q", digest.String())
	}
	imageDir := filepath.Join(store, "images", key)
	rootfs := filepath.Join(imageDir, "rootfs")
	if rec, err := readRecord(filepath.Join(imageDir, "image.json")); err == nil {
		if info, statErr := os.Stat(rootfs); statErr == nil && info.IsDir() {
			if rec.Reference == "" {
				rec.Reference = rawReference
			}
			if !contains(rec.References, rawReference) {
				rec.References = append(rec.References, rawReference)
			}
			rec.PulledAt = time.Now().UTC()
			if err := writeRecord(filepath.Join(imageDir, "image.json"), rec); err != nil {
				return Record{}, err
			}
			return rec, nil
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return Record{}, statErr
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Record{}, err
	}
	if err := os.MkdirAll(filepath.Join(store, "blobs", "sha256"), 0700); err != nil {
		return Record{}, fmt.Errorf("create image blob store: %w", err)
	}
	if err := os.MkdirAll(imageDir, 0700); err != nil {
		return Record{}, fmt.Errorf("create image directory: %w", err)
	}
	tempRoot, err := os.MkdirTemp(imageDir, ".rootfs-*")
	if err != nil {
		return Record{}, fmt.Errorf("create temporary rootfs: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	if err := os.Chmod(tempRoot, 0755); err != nil {
		return Record{}, err
	}
	directoryModes := make(map[string]os.FileMode)

	layers, err := image.Layers()
	if err != nil {
		return Record{}, fmt.Errorf("list image layers: %w", err)
	}
	if len(config.RootFS.DiffIDs) != len(layers) {
		return Record{}, fmt.Errorf("image config has %d layer diff IDs but manifest has %d layers", len(config.RootFS.DiffIDs), len(layers))
	}
	configDigest, err := image.ConfigName()
	if err != nil {
		return Record{}, fmt.Errorf("read image config digest: %w", err)
	}
	record := Record{Reference: rawReference, References: []string{rawReference}, Digest: digest.String(), ImageID: configDigest.String(), OS: config.OS,
		Architecture: config.Architecture, RootFS: rootfs, PulledAt: time.Now().UTC(), Layers: make([]string, 0, len(layers))}
	for index, layer := range layers {
		layerDigest, err := layer.Digest()
		if err != nil {
			return Record{}, fmt.Errorf("read layer %d digest: %w", index, err)
		}
		blob, err := cacheLayer(store, layer, layerDigest)
		if err != nil {
			return Record{}, fmt.Errorf("cache layer %d: %w", index, err)
		}
		record.Layers = append(record.Layers, layerDigest.String())
		mediaType, err := layer.MediaType()
		if err != nil {
			return Record{}, fmt.Errorf("read layer %d media type: %w", index, err)
		}
		blobInfo, err := os.Stat(blob)
		if err != nil {
			return Record{}, fmt.Errorf("inspect cached layer %d: %w", index, err)
		}
		localLayer, err := partial.CompressedToLayer(cachedLayer{path: blob, digest: layerDigest, mediaType: mediaType, size: blobInfo.Size()})
		if err != nil {
			return Record{}, fmt.Errorf("open cached layer %d: %w", index, err)
		}
		reader, err := localLayer.Uncompressed()
		if err != nil {
			return Record{}, fmt.Errorf("decompress cached layer %d: %w", index, err)
		}
		uncompressedHash := sha256.New()
		unpackErr := applyLayer(tempRoot, io.TeeReader(reader, uncompressedHash), directoryModes)
		closeErr := reader.Close()
		if unpackErr != nil {
			return Record{}, fmt.Errorf("unpack layer %d: %w", index, unpackErr)
		}
		if closeErr != nil {
			return Record{}, fmt.Errorf("verify layer %d: %w", index, closeErr)
		}
		if got := hex.EncodeToString(uncompressedHash.Sum(nil)); got != config.RootFS.DiffIDs[index].Hex {
			return Record{}, fmt.Errorf("uncompressed layer digest mismatch at layer %d: expected %s, got sha256:%s", index, config.RootFS.DiffIDs[index].String(), got)
		}
	}
	if err := applyDirectoryModes(tempRoot, directoryModes); err != nil {
		return Record{}, fmt.Errorf("apply rootfs directory modes: %w", err)
	}
	if err := os.Rename(tempRoot, rootfs); err != nil {
		return Record{}, fmt.Errorf("publish image rootfs: %w", err)
	}
	if err := writeRecord(filepath.Join(imageDir, "image.json"), record); err != nil {
		_ = os.RemoveAll(rootfs)
		return Record{}, err
	}
	return record, nil
}

type cachedLayer struct {
	path      string
	digest    v1.Hash
	mediaType types.MediaType
	size      int64
}

func (l cachedLayer) Digest() (v1.Hash, error)            { return l.digest, nil }
func (l cachedLayer) Size() (int64, error)                { return l.size, nil }
func (l cachedLayer) MediaType() (types.MediaType, error) { return l.mediaType, nil }
func (l cachedLayer) Compressed() (io.ReadCloser, error)  { return os.Open(l.path) }

func cacheLayer(store string, layer v1.Layer, expected v1.Hash) (string, error) {
	if expected.Algorithm != "sha256" || len(expected.Hex) != 64 {
		return "", fmt.Errorf("unsupported layer digest %q", expected.String())
	}
	dir := filepath.Join(store, "blobs", "sha256")
	destination := filepath.Join(dir, expected.Hex)
	if f, err := os.Open(destination); err == nil {
		hash := sha256.New()
		_, copyErr := io.Copy(hash, f)
		closeErr := f.Close()
		if copyErr == nil && closeErr == nil && hex.EncodeToString(hash.Sum(nil)) == expected.Hex {
			return destination, nil
		}
		if err := os.Remove(destination); err != nil {
			return "", fmt.Errorf("remove corrupt cached blob: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	compressed, err := layer.Compressed()
	if err != nil {
		return "", fmt.Errorf("open compressed layer: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".blob-*")
	if err != nil {
		_ = compressed.Close()
		return "", err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temp, hash), compressed); err != nil {
		_ = temp.Close()
		_ = compressed.Close()
		return "", err
	}
	if err := compressed.Close(); err != nil {
		_ = temp.Close()
		return "", err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != expected.Hex {
		_ = temp.Close()
		return "", fmt.Errorf("compressed digest mismatch: expected sha256:%s, got sha256:%s", expected.Hex, got)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tempName, destination); err != nil {
		// Another concurrent pull may have installed the same immutable blob.
		if _, statErr := os.Stat(destination); statErr == nil {
			return destination, nil
		}
		return "", err
	}
	return destination, nil
}

func applyLayer(root string, input io.Reader, directoryModes map[string]os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(root), ".layer-*.tar")
	if err != nil {
		return fmt.Errorf("stage layer archive: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, input); err != nil {
		_ = temp.Close()
		return fmt.Errorf("stage layer archive: %w", err)
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		_ = temp.Close()
		return err
	}
	if err := applyWhiteouts(root, temp, directoryModes); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		_ = temp.Close()
		return err
	}
	err = unpackLayer(root, temp, directoryModes)
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func applyWhiteouts(root string, archive io.Reader, directoryModes map[string]os.FileMode) error {
	reader := tar.NewReader(archive)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" || name == "." {
			continue
		}
		if _, err := safeTarget(root, name); err != nil {
			return fmt.Errorf("unsafe archive path %q: %w", name, err)
		}
		base := pathpkg.Base(name)
		dir := pathpkg.Dir(name)
		if base == ".wh..wh..opq" {
			parent, err := safeTarget(root, dir)
			if err != nil {
				return err
			}
			if err := checkParents(root, parent); err != nil {
				return err
			}
			entries, err := os.ReadDir(parent)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := os.RemoveAll(filepath.Join(parent, entry.Name())); err != nil {
					return err
				}
				forgetDirectoryModes(directoryModes, pathpkg.Join(dir, entry.Name()))
			}
		} else if strings.HasPrefix(base, ".wh.") {
			victimName := strings.TrimPrefix(base, ".wh.")
			if victimName == "" {
				return fmt.Errorf("invalid whiteout entry %q", name)
			}
			target, err := safeTarget(root, pathpkg.Join(dir, victimName))
			if err != nil {
				return err
			}
			if err := checkParents(root, filepath.Dir(target)); err != nil {
				return err
			}
			if err := os.RemoveAll(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			forgetDirectoryModes(directoryModes, pathpkg.Join(dir, victimName))
		}
	}
}

func unpackLayer(root string, input io.Reader, directoryModes map[string]os.FileMode) error {
	reader := tar.NewReader(input)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" || name == "." {
			continue
		}
		if _, err := safeTarget(root, name); err != nil {
			return fmt.Errorf("unsafe archive path %q: %w", name, err)
		}
		base := pathpkg.Base(name)
		if base == ".wh..wh..opq" || strings.HasPrefix(base, ".wh.") {
			continue
		}
		target, _ := safeTarget(root, name)
		if err := ensureParents(root, filepath.Dir(target)); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if info, err := os.Lstat(target); err == nil && !info.IsDir() {
				if err := os.RemoveAll(target); err != nil {
					return err
				}
				forgetDirectoryModes(directoryModes, name)
			}
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			directoryModes[pathpkg.Clean(name)] = os.FileMode(header.Mode) & 0777
		case tar.TypeReg, tar.TypeRegA:
			if err := os.RemoveAll(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			forgetDirectoryModes(directoryModes, name)
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if !pathpkg.IsAbs(header.Linkname) {
				resolved := pathpkg.Clean(pathpkg.Join(pathpkg.Dir(name), header.Linkname))
				if resolved == ".." || strings.HasPrefix(resolved, "../") {
					return fmt.Errorf("symlink target %q escapes rootfs", header.Linkname)
				}
			}
			if err := os.RemoveAll(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			forgetDirectoryModes(directoryModes, name)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			linkTarget, err := safeTarget(root, header.Linkname)
			if err != nil {
				return fmt.Errorf("unsafe hardlink target %q: %w", header.Linkname, err)
			}
			if err := ensureParents(root, filepath.Dir(linkTarget)); err != nil {
				return err
			}
			info, err := os.Lstat(linkTarget)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("hardlink target %q is not a regular file", header.Linkname)
			}
			if err := os.RemoveAll(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			forgetDirectoryModes(directoryModes, name)
			if err := os.Link(linkTarget, target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported archive entry type %d for %q", header.Typeflag, name)
		}
	}
}

func safeTarget(root, name string) (string, error) {
	if pathpkg.IsAbs(name) {
		return "", errors.New("absolute path")
	}
	clean := pathpkg.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path escapes rootfs")
	}
	return filepath.Join(root, filepath.FromSlash(clean)), nil
}

func forgetDirectoryModes(modes map[string]os.FileMode, path string) {
	clean := pathpkg.Clean(path)
	for candidate := range modes {
		if candidate == clean || strings.HasPrefix(candidate, clean+"/") {
			delete(modes, candidate)
		}
	}
}

func applyDirectoryModes(root string, modes map[string]os.FileMode) error {
	paths := make([]string, 0, len(modes))
	for path := range modes {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		return strings.Count(paths[i], "/") > strings.Count(paths[j], "/")
	})
	for _, path := range paths {
		target, err := safeTarget(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := os.Chmod(target, modes[path]); err != nil {
			return fmt.Errorf("chmod %q: %w", path, err)
		}
	}
	return nil
}

func ensureParents(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("path escapes rootfs")
	}
	current := root
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0755); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("archive path traverses non-directory %q", current)
		}
	}
	return nil
}

func checkParents(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("path escapes rootfs")
	}
	current := root
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("archive path traverses non-directory %q", current)
		}
	}
	return nil
}

func writeRecord(path string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".image-*.json")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func readRecord(path string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("decode image metadata %q: %w", path, err)
	}
	return record, nil
}

func List(store string) ([]Record, error) {
	entries, err := os.ReadDir(filepath.Join(store, "images"))
	if errors.Is(err, os.ErrNotExist) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	var records []Record
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, err := readRecord(filepath.Join(store, "images", entry.Name(), "image.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(record.References) == 0 {
			records = append(records, record)
			continue
		}
		for _, reference := range record.References {
			alias := record
			alias.Reference = reference
			records = append(records, alias)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Reference == records[j].Reference {
			return records[i].PulledAt.After(records[j].PulledAt)
		}
		return records[i].Reference < records[j].Reference
	})
	return records, nil
}

func ResolveLocal(store, rawReference string) (Record, error) {
	ref, err := ParseReference(rawReference)
	if err != nil {
		return Record{}, err
	}
	canonical := ref.Name()
	records, err := List(store)
	if err != nil {
		return Record{}, err
	}
	var newest *Record
	for index := range records {
		record := &records[index]
		if record.Reference == rawReference || record.Reference == canonical || contains(record.References, rawReference) || contains(record.References, canonical) {
			if newest == nil || record.PulledAt.After(newest.PulledAt) {
				newest = record
			}
		}
	}
	if newest != nil {
		return *newest, nil
	}
	return Record{}, fmt.Errorf("image %q is not pulled locally; run `brain images pull %s` first", rawReference, rawReference)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
