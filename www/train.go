package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func train() error {
	var linode struct {
		ID   string `json:"id"`
		IPv6 string `json:"ipv6"`
	}

	{
		res, err := http.PostForm("https://api.linode.com/v4/linode/instances", url.Values{
			"label":  {"train"},
			"type":   {"g6-standard-2"},
			"image":  {"private/feed-train"},
			"region": {"fremont-ca"},
		})
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if err := json.NewDecoder(res.Body).Decode(&linode); err != nil {
			return err
		}
	}

	defer func() {
		req, err := http.NewRequest("DELETE", fmt.Sprintf("https://api.linode.com/v4/linode/instances/%s", linode.ID), nil)
		if err != nil {
			log.Printf("Creating DELETE request: %s", err)
			return
		}

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Sending DELETE request: %s", err)
			return
		}
		defer res.Body.Close()
	}()

	if err := exec.Command(
		"wg", "set", "wg0",
		"peer", "hROTAtct2RIdBfJdnfQUl/mMbgKnsdxB67+VakbqiBk=",
		"allowed-ips", "10.0.2.1/32",
		"endpoint", linode.IPv6+":51820",
	).Run(); err != nil {
		return err
	}

	defer func() {
		if err := exec.Command(
			"wg", "set", "wg0",
			"peer", "hROTAtct2RIdBfJdnfQUl/mMbgKnsdxB67+VakbqiBk=",
			"remove",
		).Run(); err != nil {
			log.Printf("Removing wireguard peer: %s", err)
		}
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		res, err := http.Get("http://10.0.2.1/health")
		if err != nil {
			log.Printf("Got error while waiting for trainer to become healthy: %q", err)
			continue
		}

		if res.StatusCode != http.StatusOK {
			log.Printf("/health returned status %q", res.Status)
			continue
		}

		break
	}

	var trainResult struct {
		Bin, Vec []byte
	}

	{
		res, err := http.Post("http://10.0.2.1/train", "", nil)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if err := json.NewDecoder(res.Body).Decode(&trainResult); err != nil {
			return err
		}
	}

	dir, err := ioutil.TempDir("", "train-result")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	tempModelBinPath := filepath.Join(dir, "model.bin")
	tempModelVecPath := filepath.Join(dir, "model.vec")

	if err := ioutil.WriteFile(tempModelBinPath, trainResult.Bin, 0600); err != nil {
		return err
	}

	if err := ioutil.WriteFile(tempModelVecPath, trainResult.Vec, 0600); err != nil {
		return err
	}

	if err := os.Rename(tempModelBinPath, "./model.bin"); err != nil {
		return err
	}

	if err := os.Rename(tempModelVecPath, "./model.vec"); err != nil {
		return err
	}

	return nil
}
