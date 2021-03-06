package store

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/danielfoehrkn/kubectlSwitch/types"
)

func (s *VaultStore) GetKind() types.StoreKind {
	return types.StoreKindVault
}

func (s *VaultStore) GetLogger() *logrus.Entry {
	return s.Logger
}

func (s *VaultStore) recursivePathTraversal(wg *sync.WaitGroup, searchPath string, channel chan SearchResult) {
	defer wg.Done()

	secret, err := s.Client.Logical().List(searchPath)
	if err != nil {
		channel <- SearchResult{
			KubeconfigPath: "",
			Error:          err,
		}
		return
	}

	if secret == nil {
		s.Logger.Infof("No secrets found for path %s", searchPath)
		return
	}

	items := secret.Data["keys"].([]interface{})
	for _, item := range items {
		itemPath := fmt.Sprintf("%s/%s", strings.TrimSuffix(searchPath, "/"), item)
		if strings.HasSuffix(item.(string), "/") {
			// this is another folder
			wg.Add(1)
			go s.recursivePathTraversal(wg, itemPath, channel)
		} else if item != "" {
			// found an actual secret
			channel <- SearchResult{
				KubeconfigPath: itemPath,
				Error:          err,
			}
		}
	}
}

func (s *VaultStore) StartSearch(channel chan SearchResult) {
	wg := sync.WaitGroup{}
	// start multiple recursive searches from different root paths
	for _, path := range s.vaultPaths {
		s.Logger.Debugf("discovering secrets from vault under path %q", path)

		wg.Add(1)
		go s.recursivePathTraversal(&wg, path, channel)
	}
	wg.Wait()
}

func getBytesFromSecretValue(v interface{}) ([]byte, error) {
	data, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("failed to marshal value into string")
	}

	bytes := []byte(data)

	// check if it is base64 encode - if yes use the decoded version
	base64, err := base64.StdEncoding.DecodeString(data)
	if err == nil {
		bytes = base64
	}
	return bytes, nil
}

func (s *VaultStore) GetKubeconfigForPath(path string) ([]byte, error) {
	s.Logger.Debugf("vault: getting secret for path %q", path)

	secret, err := s.Client.Logical().Read(path)
	if err != nil {
		return nil, fmt.Errorf("could not read secret with path '%s': %v", path, err)
	}

	if secret == nil {
		return nil, fmt.Errorf("no kubeconfig found for path %s", path)
	}

	if len(secret.Data) != 1 {
		return nil, fmt.Errorf("cannot read kubeconfig from %q. Only support one entry in the secret", path)
	}

	for secretKey, data := range secret.Data {
		matched, err := filepath.Match(s.KubeconfigName, secretKey)
		if err != nil {
			return nil, err
		}
		if !matched {
			return nil, fmt.Errorf("cannot read kubeconfig from %q. Key %q does not match desired kubeconfig name", path, s.KubeconfigName)
		}

		bytes, err := getBytesFromSecretValue(data)
		if err != nil {
			return nil, fmt.Errorf("cannot read kubeconfig from %q: %v", path, err)
		}
		return bytes, nil
	}
	return nil, fmt.Errorf("should not happen")
}

func (s *VaultStore) VeryKubeconfigPaths() error {
	var duplicatePath = make(map[string]*struct{})

	for _, path := range s.KubeconfigPaths {
		if path.Store != types.StoreKindVault {
			continue
		}

		// do not add duplicate paths
		if duplicatePath[path.Path] != nil {
			continue
		}
		duplicatePath[path.Path] = &struct{}{}

		_, err := s.Client.Logical().Read(path.Path)
		if err != nil {
			return err
		}

		s.vaultPaths = append(s.vaultPaths, path.Path)
	}
	return nil
}
