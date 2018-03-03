package main

import (
	"bufio"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/kljensen/snowball"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"syscall"
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

func classifiableString(item map[string]*dynamodb.AttributeValue) string {
	//title := preprocessString(*item["title"].S)
	title := *item["title"].S
	feed := *item["feed"].S
	if label, labelExists := item["label"]; labelExists {
		return fmt.Sprintf("__label__%s %s %s", *label.S, feed, title)
	}
	return fmt.Sprintf("%s %s", feed, title)
}

type classifyReq struct {
	item string
	done chan float64
}

type classifier struct {
	classifyCh chan classifyReq
	quitCh     chan struct{}
}

func newClassifier() *classifier {
	return &classifier{
		classifyCh: make(chan classifyReq),
		quitCh:     make(chan struct{}),
	}
}

func (c *classifier) classify(item string) float64 {
	done := make(chan float64)
	c.classifyCh <- classifyReq{
		item: item,
		done: done,
	}
	return <-done
}

func (c *classifier) quit() {
	c.quitCh <- struct{}{}
}

func (c *classifier) serve() error {
	dir, err := ioutil.TempDir("", "classifier")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	fifo := path.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		return err
	}

	svc := s3.New(sess)
	result, err := svc.GetObject(&s3.GetObjectInput{
		Bucket: aws.String("alec-personal"),
		Key:    aws.String("feed/model.bin"),
	})
	if err != nil {
		return err
	}
	defer result.Body.Close()

	go func() {
		fifoFile, err := os.OpenFile(fifo, os.O_WRONLY, 0000)
		if err != nil {
			log.Printf("Opening fifo: %s", err)
		}
		defer fifoFile.Close()

		if _, err := io.Copy(fifoFile, result.Body); err != nil {
			log.Printf("Copying: %s", err)
		}
	}()

	cmd := exec.Command("./fasttext", "predict-prob", fifo, "-")
	cmd.Stderr = os.Stderr
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

			switch label {
			case "__label__1":
			case "__label__0":
				prob = 1 - prob
			default:
				return fmt.Errorf("Classifier returned unknown label: %q", label)
			}

			req.done <- prob
		}
	}
}
