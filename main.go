package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/BurntSushi/toml"
	"github.com/jmoiron/sqlx"
	"github.com/kljensen/snowball"
	_ "github.com/mattn/go-sqlite3"
	"github.com/mmcdole/gofeed"
	"html/template"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var wordRe = regexp.MustCompile(`([a-zA-Z']+)`)

func splitWords(title string) []string {
	words := wordRe.FindAllString(strings.ToLower(title), -1)
	for i, word := range words {
		stem, err := snowball.Stem(word, "english", true)
		if err != nil {
			log.Printf("err %s", err)
		}
		words[i] = stem
	}
	return words
}

func refresh(db *sqlx.DB, feeds map[string]string) {
	fp := gofeed.NewParser()
	var group sync.WaitGroup
	for feed, url := range feeds {
		feed := feed
		url := url
		group.Add(1)
		go func() {
			defer group.Done()
			time.Sleep(time.Duration(rand.Intn(2000)) * time.Millisecond)
			downloaded, err := fp.ParseURL(url)
			if err != nil {
				log.Printf("Parsing feed for %q (%q): %s", feed, url, err)
				return
			}

			for _, item := range downloaded.Items {
				if item.GUID == "" {
					item.GUID = item.Link
				}

				if _, err := db.Exec(`
					INSERT OR IGNORE INTO item (loaded, feed, guid, title, link)
					VALUES (strftime('%s'), ?, ?, ?, ?)
				`, feed, item.GUID, item.Title, item.Link); err != nil {
					log.Printf("Inserting feed=%q guid=%q title=%q link=%q: %s", feed, item.GUID, item.Title, item.Link, err)
				}
			}
		}()
	}

	group.Wait()
}

type item struct {
	Feed      string `db:"feed"`
	GUID      string `db:"guid"`
	Title     string `db:"title"`
	Link      string `db:"link"`
	Judgement *bool  `db:"judgement"`
	Odds      float64
}

func (i item) classifiableString() string {
	return fmt.Sprintf("_feed_%s %s", i.Feed, strings.Join(splitWords(i.Title), " "))
}

type classifyReq struct {
	item item
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

func (c *classifier) classify(item item) float64 {
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

func (c *classifier) serve(db *sqlx.DB) error {
	dir, err := ioutil.TempDir(".", "fasttext")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	{
		trainingData, err := os.Create(path.Join(dir, "data"))
		if err != nil {
			return err
		}
		defer trainingData.Close()

		rows, err := db.Queryx(`
			SELECT judgement, feed, title
			FROM item
			WHERE judgement IS NOT NULL
			ORDER BY RANDOM()
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item item
			if err := rows.StructScan(&item); err != nil {
				return err
			}

			var label string
			if *item.Judgement {
				label = "1"
			} else {
				label = "0"
			}

			if _, err := fmt.Fprintf(trainingData, "__label__%s %s\n", label, item.classifiableString()); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}

		if err := trainingData.Close(); err != nil {
			return err
		}
	}

	{
		cmd := exec.Command(
			"fasttext", "supervised",
			"-input", "data",
			"-output", "model",
			"-epoch", "20",
			"-lr", "0.4",
			"-wordNgrams", "2",
		)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		cmd.Dir = dir
		trainingData, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		defer trainingData.Close()

		if err := cmd.Start(); err != nil {
			return err
		}

		if err := trainingData.Close(); err != nil {
			return err
		}

		if err := cmd.Wait(); err != nil {
			return err
		}
	}

	cmd := exec.Command("fasttext", "predict-prob", "model.bin", "-")
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer stdin.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)

	for {
		select {
		case <-c.quitCh:
			return nil
		case req := <-c.classifyCh:
			s := req.item.classifiableString()
			if _, err := fmt.Fprintf(stdin, "%s\n", s); err != nil {
				return err
			}

			if !scanner.Scan() {
				return fmt.Errorf("Expected response: %s", scanner.Err())
			}

			var label string
			var prob float64
			if _, err := fmt.Sscanf(scanner.Text(), "%s %f\n", &label, &prob); err != nil {
				return err
			}

			switch label {
			case "__label__1":
			case "__label__0":
				prob = 1 - prob
			default:
				return fmt.Errorf("fasttext returned unknown odds: %q", label)
			}

			req.done <- prob
		}
	}
}

func main() {
	var addr string
	flag.StringVar(&addr, "addr", ":8080", "Address to listen on")
	flag.Parse()

	templ, err := template.ParseFiles("template.html")
	if err != nil {
		panic(err)
	}

	var config struct {
		Feeds map[string]string
	}

	if _, err := toml.DecodeFile("config.toml", &config); err != nil {
		panic(err)
	}

	db, err := sqlx.Open("sqlite3", "db.sqlite3")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	http.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			panic(err)
		}

		var judgements map[string]bool
		if err := json.Unmarshal([]byte(r.Form.Get("judgements")), &judgements); err != nil {
			panic(err)
		}

		for guid, judgement := range judgements {
			if _, err := db.Exec(`
					UPDATE item
					SET judgement = ?
					WHERE guid = ?
				`, judgement, guid); err != nil {
				panic(err)
			}
		}

		http.Redirect(w, r, "/", http.StatusFound)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			return
		}

		var lastLoadedUnix *int64
		if err := db.QueryRow(`SELECT MAX(loaded) FROM item`).Scan(&lastLoadedUnix); err != nil {
			panic(err)
		}

		//if lastLoadedUnix == nil || time.Since(time.Unix(*lastLoadedUnix, 0)) >= 10*time.Minute {
		//	refresh(db, config.Feeds)
		//}

		c := newClassifier()

		go func() {
			if err := c.serve(db); err != nil {
				panic(err)
			}
		}()
		defer c.quit()

		items := make([]item, 0)
		rows, err := db.Queryx(`
			SELECT feed, guid, title, link, judgement
			FROM item
			WHERE judgement IS NULL
			ORDER BY RANDOM()
		`)
		if err != nil {
			panic(err)
		}
		defer rows.Close()
		for rows.Next() {
			var item item
			if err := rows.StructScan(&item); err != nil {
				panic(err)
			}
			odds := c.classify(item)
			item.Odds = odds
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			panic(err)
		}

		sort.Slice(items, func(i, j int) bool {
			return items[i].Odds > items[j].Odds
		})

		const maxItems = 3
		shownItems := items
		if len(shownItems) > maxItems {
			shownItems = shownItems[:maxItems]
		}

		if err := templ.ExecuteTemplate(w, "index", struct {
			Items  []item
			Shown  int
			Elided int
		}{
			Items:  shownItems,
			Shown:  len(shownItems),
			Elided: len(items) - len(shownItems),
		}); err != nil {
			panic(err)
		}
	})

	log.Printf("Listening on %s", addr)
	http.ListenAndServe(addr, nil)
}
