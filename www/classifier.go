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
}

func classifiableString(item feedItem) string {
	//title := preprocessString(item.title)
	return fmt.Sprintf("%s %s", item.Feed, item.Title)
}

type classifyReq struct {
	item string
	done chan float64
}

type classifier struct {
	zeroMode   bool
	classifyCh chan classifyReq
	quitCh     chan struct{}
	doneCh     chan error
}

func newClassifier() *classifier {
	if _, err := os.Stat("model.bin"); os.IsNotExist(err) {
		return &classifier{
			zeroMode: true,
		}
	}

	c := &classifier{
		classifyCh: make(chan classifyReq),
		quitCh:     make(chan struct{}),
	}
	go c.serve()
	return c
}

func (c *classifier) classify(item string) float64 {
	if c.zeroMode {
		return 0
	}

	done := make(chan float64)
	c.classifyCh <- classifyReq{
		item: item,
		done: done,
	}
	return <-done
}

func (c *classifier) stop() error {
	if c.zeroMode {
		return nil
	}

	c.quitCh <- struct{}{}
	return <-c.doneCh
}

func (c *classifier) serve() {
	dir, err := ioutil.TempDir("", "classifier")
	if err != nil {
		c.doneCh <- fmt.Errorf("Creating classifier temporary directory: %s", err)
		return
	}
	defer os.RemoveAll(dir)

	cmd := exec.Command("./fasttext", "predict-prob", "model.bin", "-")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		c.doneCh <- fmt.Errorf("Creating stderr pipe to classifier: %s", err)
		return
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
		c.doneCh <- fmt.Errorf("Creating stdin pipe to classifier: %s", err)
		return
	}
	defer stdin.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.doneCh <- fmt.Errorf("Creating stdout pipe to classifier: %s", err)
		return
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		c.doneCh <- fmt.Errorf("Starting classifier: %s", err)
		return
	}

	waitCh := make(chan error)

	go func() {
		log.Printf("Waiting for classifier command to exit...")
		err := cmd.Wait()
		log.Printf("Classifier exited: %s", err)
		waitCh <- err
	}()

	scanner := bufio.NewScanner(stdout)

	for {
		log.Printf("Classifier waiting for input...")
		select {
		case err := <-waitCh:
			if err == nil {
				c.doneCh <- fmt.Errorf("Classifier process closed unexpectedly")
				return
			} else {
				c.doneCh <- err
				return
			}
		case <-c.quitCh:
			if err := stdin.Close(); err != nil {
				c.doneCh <- err
			}

			c.doneCh <- <-waitCh
			return
		case req := <-c.classifyCh:
			if _, err := fmt.Fprintln(stdin, req.item); err != nil {
				c.doneCh <- fmt.Errorf("Sending item to classifier: %s", err)
				return
			}

			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					c.doneCh <- fmt.Errorf("Reading classifier response: %s", scanner.Err())
					return
				}
				c.doneCh <- fmt.Errorf("Reading classifier response: EOF")
				return
			}

			var label string
			var prob float64
			if _, err := fmt.Sscanf(scanner.Text(), "%s %f\n", &label, &prob); err != nil {
				c.doneCh <- fmt.Errorf("Scanning classifier response %q: %s", scanner.Text(), err)
				return
			}

			if label == "__label__0" {
				prob = 1 - prob
			} else if label != "__label__1" {
				c.doneCh <- fmt.Errorf("Classifier returned unknown label: %q", label)
				return
			}

			req.done <- prob
		}
	}
}
