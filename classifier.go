package main

import (
	"bufio"
	"fmt"
	"github.com/kljensen/snowball"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var wordRe = regexp.MustCompile(`([a-zA-Z']+)`)

func preprocessString(title string) string {
	words := wordRe.FindAllString(strings.ToLower(title), -1)
	for i, word := range words {
		stem, err := snowball.Stem(word, "english", true)
		if err != nil {
			log.Printf("stemming %q: %s", word, err)
		}
		words[i] = stem
	}
	return strings.Join(words, " ")
}

type feedItem struct {
	GUID  string
	Feed  string
	Link  string
	Title string
	Score float64
	Label string
}

func classifiableString(item feedItem) string {
	//title := preprocessString(item.title)
	if item.Label != "" {
		return fmt.Sprintf("__label__%s %s %s", item.Label, item.Feed, item.Title)
	}
	return fmt.Sprintf("%s %s", item.Feed, item.Title)
}

type classifyReq struct {
	item string
	done chan float64
}

type classifier interface {
	classify(item string) float64
}

type zeroClassifier struct{}

func (c *zeroClassifier) classify(item string) float64 {
	return 0
}

type realClassifier struct {
	classifyCh chan classifyReq
	quitCh     chan struct{}
}

func newClassifier() *realClassifier {
	return &realClassifier{
		classifyCh: make(chan classifyReq),
		quitCh:     make(chan struct{}),
	}
}

func (c *realClassifier) classify(item string) float64 {
	done := make(chan float64)
	c.classifyCh <- classifyReq{
		item: item,
		done: done,
	}
	return <-done
}

func (c *realClassifier) quit() {
	c.quitCh <- struct{}{}
}

func (c *realClassifier) serve() error {
	dir, err := ioutil.TempDir("", "classifier")
	if err != nil {
		return fmt.Errorf("Creating classifier temporary directory: %s", err)
	}
	defer os.RemoveAll(dir)

	cmd := exec.Command("./fasttext", "predict-prob", "model.bin", "-")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("Creating stderr pipe to classifier: %s", err)
	}

	go func() {
		scanner := bufio.NewScanner(stderr)

		for scanner.Scan() {
			log.Printf("Classifier: %q", scanner.Text())
		}

		if err := scanner.Err(); err != nil {
			log.Printf("Scanning classifier stderr: %s", err)
		} else {
			log.Printf("Scanning classifier stderr: EOF")
		}
	}()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("Creating stdin pipe to classifier: %s", err)
	}
	defer stdin.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("Creating stdout pipe to classifier: %s", err)
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Starting classifier: %s", err)
	}
	defer func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("Classifier: %s", err)
		}
	}()

	scanner := bufio.NewScanner(stdout)

	for {
		select {
		case <-c.quitCh:
			return nil
		case req := <-c.classifyCh:
			if _, err := fmt.Fprintln(stdin, req.item); err != nil {
				return fmt.Errorf("Sending item to classifier: %s", err)
			}

			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return fmt.Errorf("Reading classifier response: %s", scanner.Err())
				}
				return fmt.Errorf("Reading classifier response: EOF")
			}

			var label string
			var prob float64
			if _, err := fmt.Sscanf(scanner.Text(), "%s %f\n", &label, &prob); err != nil {
				return fmt.Errorf("Scanning classifier response %q: %s", scanner.Text(), err)
			}

			if label == "__label__0" {
				prob = 1 - prob
			} else if label != "__label__1" {
				return fmt.Errorf("Classifier returned unknown label: %q", label)
			}

			req.done <- prob
		}
	}
}
