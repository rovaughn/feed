package main

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
)

func train(db *sql.DB) error {
	tempDir, err := ioutil.TempDir("", "train")
	if err != nil {
		return fmt.Errorf("Creating temporary directory for training: %s", err)
	}
	defer os.RemoveAll(tempDir)

	dataFile, err := os.Create(filepath.Join(tempDir, "data"))
	if err != nil {
		return fmt.Errorf("Creating data file: %s", err)
	}
	defer dataFile.Close()

	trainingDataFile, err := os.Create(filepath.Join(tempDir, "training-data"))
	if err != nil {
		return fmt.Errorf("Creating training data file: %s", err)
	}
	defer trainingDataFile.Close()

	testDataFile, err := os.Create(filepath.Join(tempDir, "test-data"))
	if err != nil {
		return fmt.Errorf("Creating testing data file: %s", err)
	}
	defer testDataFile.Close()

	{
		rows, err := db.Query(`
			SELECT judgement, feed, title
			FROM item
			WHERE judgement IS NOT NULL
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		i := 0

		for rows.Next() {
			var judgement bool
			var feed, title string
			if err := rows.Scan(&judgement, &feed, &title); err != nil {
				return err
			}

			var line []byte
			if judgement {
				line = []byte(fmt.Sprintf("__label__1 %s %s\n", feed, title))
			} else {
				line = []byte(fmt.Sprintf("__label__0 %s %s\n", feed, title))
			}

			if _, err := dataFile.Write(line); err != nil {
				return err
			}

			if i%5 == 0 {
				if _, err := testDataFile.Write(line); err != nil {
					return err
				}
			} else {
				if _, err := trainingDataFile.Write(line); err != nil {
					return err
				}
			}

			i++
		}

		if err := rows.Err(); err != nil {
			return err
		}
	}

	if err := dataFile.Close(); err != nil {
		return err
	}

	if err := trainingDataFile.Close(); err != nil {
		return err
	}

	if err := testDataFile.Close(); err != nil {
		return err
	}

	fastTextPath, err := filepath.Abs("fasttext")
	if err != nil {
		return err
	}

	{
		cmd := exec.Command(
			fastTextPath, "supervised",
			"-input", "training-data",
			"-output", "test-model",
			"-epoch", "20",
			"-lr", "0.4",
			"-wordNgrams", "1",
			"-dim", "300",
			"-pretrainedVectors", "wiki-news-300d-1M.vec",
		)
		cmd.Dir = tempDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("Training model with test data: %s: %q", err, out)
		}
	}

	{
		cmd := exec.Command("fasttext", "test", "test-model.bin", "test-data")
		cmd.Dir = tempDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("Testing model against test data: %s: %q", err, out)
		}
	}

	{
		cmd := exec.Command(
			"fasttext", "supervised",
			"-input", "data",
			"-output", "model",
			"-epoch", "20",
			"-lr", "0.4",
			"-wordNgrams", "1",
			"-dim", "300",
			"-pretrainedVectors", "wiki-news-300d-1M.vec",
		)
		cmd.Dir = tempDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("Training actual model: %s: %q", err, out)
		}
	}

	if err := os.Rename(filepath.Join(tempDir, "model.bin"), "."); err != nil {
		return err
	}

	if err := os.Rename(filepath.Join(tempDir, "model.vec"), "."); err != nil {
		return err
	}

	return nil
}
