package main

import (
	"database/sql"
	"github.com/mmcdole/gofeed"
	"log"
	"math/rand"
	"net/url"
	"strconv"
	"sync"
	"time"
)

func scrapeFeed(link string) ([]feedItem, error) {
	fp := gofeed.NewParser()
	result, err := fp.ParseURL(link)
	if err != nil {
		return nil, err
	}

	items := make([]feedItem, 0)
	for _, i := range result.Items {
		if i.GUID == "" {
			i.GUID = i.Link
		}

		items = append(items, feedItem{
			GUID:  i.GUID,
			Link:  i.Link,
			Title: i.Title,
		})
	}

	return items, nil
}

func refresh(classifier classifier, db *sql.DB) error {
	rows, err := db.Query(`
		SELECT feed, link
		FROM feed
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var group sync.WaitGroup
	for rows.Next() {
		var feed, link string
		if err := rows.Scan(&feed, &link); err != nil {
			return err
		}

		group.Add(1)
		go func() {
			defer group.Done()
			time.Sleep(time.Duration(rand.Intn(2000)) * time.Millisecond)

			parsedLink, err := url.Parse(link)
			if err != nil {
				log.Printf("Parsing feed link %q: %s", link, err)
				return
			}

			var items []feedItem

			if parsedLink.Host == "news.ycombinator.com" {
				values := parsedLink.Query()
				minScore, _ := strconv.Atoi(values.Get("min_score"))
				items, err = scrapeHN(minScore)
			} else {
				items, err = scrapeFeed(link)
			}

			if err != nil {
				log.Printf("Scraping %q: %s", link, err)
			}

			for _, item := range items {
				score := classifier.classify(classifiableString(item))

				if _, err := db.Exec(`
					UPSERT INTO item (guid, judgement, score, feed, title, link)
					VALUES (?, NULL, ?, ?, ?, ?)
				`, item.GUID, score, feed, item.Title, item.Link); err != nil {
					log.Printf("Inserting item from feed: %s", err)
				}
			}
		}()
	}

	if err := rows.Err(); err != nil {
		return err
	}

	group.Wait()
	log.Printf("Done")

	return nil
}
