package store

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	"github.com/karrick/godirwalk"
	"github.com/sirupsen/logrus"

	"github.com/danielfoehrkn/kubectlSwitch/types"
)

func (s *FilesystemStore) GetKind() types.StoreKind {
	return types.StoreKindFilesystem
}

func (s *FilesystemStore) GetLogger() *logrus.Entry {
	return s.Logger
}

func (s *FilesystemStore) StartSearch(channel chan SearchResult) {
	for _, path := range s.kubeconfigFilepaths {
		channel <- SearchResult{
			KubeconfigPath: path,
			Error:          nil,
		}
	}

	wg := sync.WaitGroup{}
	for _, path := range s.kubeconfigDirectories {
		wg.Add(1)
		go s.searchDirectory(&wg, path, channel)
	}
	wg.Wait()
}

func (s *FilesystemStore) searchDirectory(wg *sync.WaitGroup, searchPath string, channel chan SearchResult) {
	defer wg.Done()

	if err := godirwalk.Walk(searchPath, &godirwalk.Options{
		Callback: func(osPathname string, _ *godirwalk.Dirent) error {
			fileName := filepath.Base(osPathname)
			matched, err := filepath.Match(s.KubeconfigName, fileName)
			if err != nil {
				return err
			}
			if matched {
				channel <- SearchResult{
					KubeconfigPath: osPathname,
					Error:          nil,
				}
			}
			return nil
		},
		Unsorted: false, // (optional) set true for faster yet non-deterministic enumeration
	}); err != nil {
		channel <- SearchResult{
			KubeconfigPath: "",
			Error:          fmt.Errorf("failed to find kubeconfig files in directory: %v", err),
		}
	}
}

func (s *FilesystemStore) GetKubeconfigForPath(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

func (s *FilesystemStore) VeryKubeconfigPaths() error {
	var (
		duplicatePath              = make(map[string]*struct{})
		validKubeconfigFilepaths   []string
		validKubeconfigDirectories []string
		usr, _                     = user.Current()
		homeDir                    = usr.HomeDir
	)

	for _, path := range s.KubeconfigPaths {
		if path.Store != types.StoreKindFilesystem {
			continue
		}

		// do not add duplicate paths
		if duplicatePath[path.Path] != nil {
			continue
		}
		duplicatePath[path.Path] = &struct{}{}

		kubeconfigPath := path.Path
		if kubeconfigPath == "~" {
			kubeconfigPath = homeDir
		} else if strings.HasPrefix(kubeconfigPath, "~/") {
			// Use strings.HasPrefix so we don't match paths like
			// "/something/~/something/"
			kubeconfigPath = filepath.Join(homeDir, kubeconfigPath[2:])
		}

		info, err := os.Stat(kubeconfigPath)
		if os.IsNotExist(err) {
			return fmt.Errorf("the configured kubeconfig directory %q does not exist", path)
		} else if err != nil {
			return fmt.Errorf("failed to read from the configured kubeconfig directory %q: %v", path, err)
		}

		if info.IsDir() {
			validKubeconfigDirectories = append(validKubeconfigDirectories, kubeconfigPath)
			continue
		}
		validKubeconfigFilepaths = append(validKubeconfigFilepaths, kubeconfigPath)
	}

	if len(validKubeconfigDirectories) == 0 && len(validKubeconfigFilepaths) == 0 {
		return fmt.Errorf("none of the %d specified kubeconfig path(s) exist. Either specifiy an existing path via flag '--kubeconfig-path' or in the switch config file", len(s.KubeconfigPaths))
	}
	s.kubeconfigDirectories = validKubeconfigDirectories
	s.kubeconfigFilepaths = validKubeconfigFilepaths
	return nil
}
