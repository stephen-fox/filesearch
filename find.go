package filesearch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FindUniqueFilesConfig configures a FindUniqueFiles function.
type FindUniqueFilesConfig struct {
	// TargetDirPath is the directory to search.
	TargetDirPath string

	// Recursive, when true, will search TargetDirPath recursively.
	Recursive bool

	// AllowDupes, when true, will not track file hashes and will
	// always report files as never being seen.
	AllowDupes bool

	// HasherFn returns a hash.Hash to use when hashing a file.
	//
	// sha256.New() is used if nil.
	HasherFn func() hash.Hash

	// IncludeFileFn is called when a file is encountered during
	// the search. If the function returns 'true', the file will
	// be included. Otherwise, the file will be ignored.
	IncludeFileFn func(fullFilePath string) (shouldInclude bool)

	// FoundFileFn is called when IncludeFileFn returns true.
	FoundFileFn func(StatefulFileInfo) error
}

func (o FindUniqueFilesConfig) Validate() error {
	if o.IncludeFileFn == nil {
		return fmt.Errorf("IncludeFileFn cannot be nil")
	} else if o.FoundFileFn == nil {
		return fmt.Errorf("FoundFileFn cannot be nil")
	}

	return nil
}

// pickHasher returns either the user-provided hasher function,
// or a sane-default.
func (o FindUniqueFilesConfig) pickHasher() hash.Hash {
	if o.HasherFn == nil {
		return sha256.New()
	}

	return o.HasherFn()
}

// StatefulFileInfo expands on os.FileInfo, including additional information
// about the state of the file.
type StatefulFileInfo struct {
	// AlreadySeen is true if the file has been encountered before.
	//
	// This is always false if FindUniqueFilesConfig.AllowDupes is true.
	AlreadySeen bool

	// FilePath is the absolute file path of the file.
	FilePath string

	// PreviousFilePath is the file path where the file has been
	// previously encountered. If the file has not been previously
	// encountered, then this is an empty string.
	PreviousFilePath string

	// ParentDirPath is the parent directory of the file.
	ParentDirPath string

	// Hash is the hash string of the file.
	//
	// This is an empty string if FindUniqueFilesConfig.AllowDupes is true.
	Hash string

	// Info is the file's os.FileInfo.
	Info os.FileInfo

	// AbsSearchDirPath is the absolute path of the directory
	// that was initially searched.
	AbsSearchDirPath string
}

// FindUniqueFiles searches a given directory for files using the
// provided config by wrapping filepath.Walk().
//
// The main feature of this function is that it can ignore duplicate files.
// This behavior can be adjusted as desired in config.
func FindUniqueFiles(config FindUniqueFilesConfig) error {
	sfw, err := newFileWalker(config)
	if err != nil {
		return err
	}

	return sfw.search()
}

func newFileWalker(config FindUniqueFilesConfig) (*statefulFileWalker, error) {
	err := config.Validate()
	if err != nil {
		return nil, err
	}

	absTargetDirPath, err := filepath.Abs(config.TargetDirPath)
	if err != nil {
		return nil, err
	}

	return &statefulFileWalker{
		absTargetDirPath:     absTargetDirPath,
		fileHashesToPrevious: make(map[string]string),
		config:               config,
	}, nil
}

type statefulFileWalker struct {
	absTargetDirPath     string
	fileHashesToPrevious map[string]string
	config               FindUniqueFilesConfig
}

func (o *statefulFileWalker) search() error {
	return filepath.Walk(o.absTargetDirPath, o.fileWalkFunc)
}

func (o *statefulFileWalker) fileWalkFunc(filePath string, info os.FileInfo, err error) error {
	// Gotta check the error provided by the last call.
	if err != nil {
		return err
	}

	if !o.config.Recursive && info.IsDir() && filePath != o.absTargetDirPath {
		return filepath.SkipDir
	}

	// TODO: Non-regular files (Windows shortcut) support.
	if info.IsDir() || !info.Mode().IsRegular() {
		return nil
	}

	if !o.config.IncludeFileFn(filePath) {
		return nil
	}

	var hasBeenSeen bool
	var fileHash string
	var previousFilePath string
	if !o.config.AllowDupes {
		fileHash, err = hashFile(filePath, o.config.pickHasher())
		if err != nil {
			return fmt.Errorf("failed to hash file '%s' - %w", filePath, err)
		}

		previousFilePath, hasBeenSeen = o.fileHashesToPrevious[fileHash]
		if !hasBeenSeen {
			o.fileHashesToPrevious[fileHash] = strings.TrimPrefix(filePath, o.absTargetDirPath)
		}
	}

	return o.config.FoundFileFn(StatefulFileInfo{
		AlreadySeen:      hasBeenSeen,
		PreviousFilePath: previousFilePath,
		FilePath:         filePath,
		ParentDirPath:    filepath.Dir(filePath),
		Hash:             fileHash,
		Info:             info,
		AbsSearchDirPath: o.absTargetDirPath,
	})
}

func hashFile(filePath string, hasher hash.Hash) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	_, err = io.Copy(hasher, f)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
