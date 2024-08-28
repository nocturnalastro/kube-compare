package compare

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"strings"

	"sigs.k8s.io/yaml"
)

func hashReference(fs fs.FS, ref Reference) (string, error) {
	hash := sha256.New()
	refBytes, err := yaml.Marshal(ref)
	if err != nil {
		return "", fmt.Errorf("failed to hash reference: %w", err)
	}
	hash.Write(refBytes)
	err = hashFiles(fs, getReferenceFiles(ref), hash)
	if err != nil {
		return "", err
	}
	return formatHash(hash), nil
}

func getReferenceFiles(ref Reference) []string {
	filesToHash := make([]string, 0)
	filesToHash = append(filesToHash, ref.TemplateFunctionFiles...)
	for _, t := range ref.getTemplates() {
		filesToHash = append(filesToHash, t.Path)
	}
	return filesToHash
}

func hashFiles(fs fs.FS, filenames []string, hash hash.Hash) error {
	for _, fname := range filenames {
		f, err := fs.Open(fname)
		if err != nil {
			return fmt.Errorf("failed to open %s hash can not be trusted", fname)
		}
		io.Copy(hash, f)
	}
	return nil
}

func formatHash(hash hash.Hash) string {
	str := strings.ToUpper(hex.EncodeToString(hash.Sum(nil)))
	return fmt.Sprintf(
		"%s-%s-%s-%s-%s-%s-%s-%s",
		str[:8], str[8:16], str[16:24], str[24:32],
		str[32:40], str[40:48], str[48:56], str[56:],
	)
}
