package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	_ "github.com/lib/pq"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	db, err := sql.Open("postgres", "postgresql://localhost:26257/feed?sslmode=disable")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	http.HandleFunc("/train", func(w http.ResponseWriter, r *http.Request) {
		result, err := train(db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := json.NewEncoder(w).Encode(result); err != nil {
			panic(err)
		}
	})

	listenAddress := os.Getenv("listen_address")
	log.Printf("Listening on %q", listenAddress)
	panic(http.ListenAndServe(listenAddress, nil))
}

type trainResult struct {
	Bin, Vec []byte
}

func train(db *sql.DB) (*trainResult, error) {
	tempDir, err := ioutil.TempDir("", "train")
	if err != nil {
		return nil, fmt.Errorf("Creating temporary directory for training: %s", err)
	}
	defer os.RemoveAll(tempDir)

	dataFile, err := os.Create(filepath.Join(tempDir, "data"))
	if err != nil {
		return nil, fmt.Errorf("Creating data file: %s", err)
	}
	defer dataFile.Close()

	trainingDataFile, err := os.Create(filepath.Join(tempDir, "training-data"))
	if err != nil {
		return nil, fmt.Errorf("Creating training data file: %s", err)
	}
	defer trainingDataFile.Close()

	testDataFile, err := os.Create(filepath.Join(tempDir, "test-data"))
	if err != nil {
		return nil, fmt.Errorf("Creating testing data file: %s", err)
	}
	defer testDataFile.Close()

	log.Printf("Training: Collecting data into files...")

	{
		rows, err := db.Query(`
			SELECT judgement, feed, title
			FROM item
			WHERE judgement IS NOT NULL
		`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		i := 0

		for rows.Next() {
			var judgement bool
			var feed, title string
			if err := rows.Scan(&judgement, &feed, &title); err != nil {
				return nil, err
			}

			var line []byte
			if judgement {
				line = []byte(fmt.Sprintf("__label__1 %s %s\n", feed, title))
			} else {
				line = []byte(fmt.Sprintf("__label__0 %s %s\n", feed, title))
			}

			if _, err := dataFile.Write(line); err != nil {
				return nil, err
			}

			if i%5 == 0 {
				if _, err := testDataFile.Write(line); err != nil {
					return nil, err
				}
			} else {
				if _, err := trainingDataFile.Write(line); err != nil {
					return nil, err
				}
			}

			i++
		}

		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	if err := dataFile.Close(); err != nil {
		return nil, err
	}

	if err := trainingDataFile.Close(); err != nil {
		return nil, err
	}

	if err := testDataFile.Close(); err != nil {
		return nil, err
	}

	log.Printf("Training: Done collecting data into files")

	pretrainedVectorsPath, err := filepath.Abs("wiki-news-300d-1M.vec")
	if err != nil {
		return nil, err
	}

	fastTextPath, err := filepath.Abs("fasttext")
	if err != nil {
		return nil, err
	}

	{
		log.Printf("Training: training test model")
		cmd := exec.Command(
			"/usr/bin/time", "-v",
			fastTextPath, "supervised",
			"-input", "training-data",
			"-output", "test-model",
			"-epoch", "20",
			"-lr", "0.4",
			"-wordNgrams", "1",
			"-dim", "300",
			"-pretrainedVectors", pretrainedVectorsPath,
		)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		cmd.Dir = tempDir
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("Training model with test data: %s", err)
		}
		log.Printf("Training: done training test model")
	}

	{
		log.Printf("Training: testing test model")
		cmd := exec.Command(
			"/usr/bin/time", "-v",
			"fasttext", "test", "test-model.bin", "test-data",
		)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		cmd.Dir = tempDir
		err := cmd.Run()
		log.Printf("Training: done training test model: %s", err)
	}

	{
		log.Printf("Training: training real model")
		cmd := exec.Command(
			"/usr/bin/time", "-v",
			"fasttext", "supervised",
			"-input", "data",
			"-output", "model",
			"-epoch", "20",
			"-lr", "0.4",
			"-wordNgrams", "1",
			"-dim", "300",
			"-pretrainedVectors", pretrainedVectorsPath,
		)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		cmd.Dir = tempDir
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("Training actual model: %s", err)
		}
		log.Printf("Training: done training real model")
	}

	binData, err := ioutil.ReadFile("model.bin")
	if err != nil {
		return nil, err
	}

	vecData, err := ioutil.ReadFile("model.bin")
	if err != nil {
		return nil, err
	}

	return &trainResult{Bin: binData, Vec: vecData}, nil
}
