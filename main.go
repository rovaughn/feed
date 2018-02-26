package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/BurntSushi/toml"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/mmcdole/gofeed"
	"html/template"
	"log"
	"math"
	"math/rand"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var wordRe = regexp.MustCompile(`([a-zA-Z]+)`)

func splitWords(title string) []string {
	words := wordRe.FindAllString(title, -1)
	for i, word := range words {
		words[i] = strings.ToLower(word)
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

type odds struct {
	a, b int
}

func (o odds) add(p odds) odds {
	return odds{
		a: o.a + p.a,
		b: o.b + p.b,
	}
}

func (o odds) log() float64 {
	return math.Log(float64(o.a+1) / float64(o.b+1))
}

type item struct {
	Feed      string `db:"feed"`
	GUID      string `db:"guid"`
	Title     string `db:"title"`
	Link      string `db:"link"`
	Judgement *bool  `db:"judgement"`
	Odds      float64
}

func (item item) features() map[string]bool {
	result := make(map[string]bool)
	result["feed:"+item.Feed] = true
	for _, word := range splitWords(item.Title) {
		result["feed:"+item.Feed+"/word:"+word] = true
	}
	return result
}

type classifier struct {
	initial  float64
	features map[string]float64
}

func createClassifier(db *sqlx.DB) (*classifier, error) {
	var initialOdds odds
	features := make(map[string]odds)

	rows, err := db.Queryx(`
			SELECT feed, guid, title, link, judgement
			FROM item
			WHERE judgement IS NOT NULL
		`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item item
		if err := rows.StructScan(&item); err != nil {
			return nil, err
		}

		var incr odds
		if *item.Judgement {
			incr = odds{1, 0}
		} else {
			incr = odds{0, 1}
		}

		initialOdds = initialOdds.add(incr)
		itemFeatures := item.features()
		for feature := range itemFeatures {
			conditions := strings.Split(feature, "/")
			if len(conditions) == 1 || len(conditions) == 2 && itemFeatures[conditions[0]] {
				features[feature] = features[feature].add(incr)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	featuresLog := make(map[string]float64, len(features))
	for feature, odds := range features {
		featuresLog[feature] = odds.log()
	}

	return &classifier{
		initial:  initialOdds.log(),
		features: featuresLog,
	}, nil
}

func (c *classifier) classify(item *item) {
	item.Odds = c.initial
	for feature := range item.features() {
		item.Odds += c.features[feature]
	}
}

func toLogOdds(odds map[string]odds) map[string]float64 {
	result := make(map[string]float64, len(odds))
	for k, v := range odds {
		result[k] = v.log()
	}
	return result
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

	http.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		c, err := createClassifier(db)
		if err != nil {
			panic(err)
		}

		fmt.Fprintf(w, "initial = %f\n", c.initial)
		fmt.Fprintf(w, "\n")

		sorted := make([]string, 0, len(c.features))
		for name := range c.features {
			sorted = append(sorted, name)
		}
		sort.Slice(sorted, func(i, j int) bool {
			return c.features[sorted[i]] > c.features[sorted[j]]
		})
		for _, name := range sorted {
			fmt.Fprintf(w, "%s = %f\n", name, c.features[name])
		}
	})

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
		var lastLoadedUnix *int64
		if err := db.QueryRow(`SELECT MAX(loaded) FROM item`).Scan(&lastLoadedUnix); err != nil {
			panic(err)
		}

		if lastLoadedUnix == nil || time.Since(time.Unix(*lastLoadedUnix, 0)) >= time.Hour {
			refresh(db, config.Feeds)
		}

		c, err := createClassifier(db)
		if err != nil {
			panic(err)
		}

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
			c.classify(&item)
			if item.Odds > -1 {
				items = append(items, item)
			}
		}
		if err := rows.Err(); err != nil {
			panic(err)
		}

		sort.Slice(items, func(i, j int) bool {
			return items[i].Odds > items[j].Odds
		})

		const maxItems = 12
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
