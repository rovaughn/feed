package main

import (
	"database/sql"
	_ "github.com/lib/pq"
	"html/template"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

func updateScores(classifier *classifier, db *sql.DB) error {
	log.Printf("Updating scores...")
	defer log.Printf("Done updating scores")

	rows, err := db.Query(`
		SELECT guid, title, feed
		FROM item
		WHERE judgement IS NULL
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item feedItem
		if err := rows.Scan(&item.GUID, &item.Title, &item.Feed); err != nil {
			return err
		}
		score := classifier.classify(classifiableString(item))

		if _, err := db.Exec(`
			UPDATE item
			SET score = $1
			WHERE guid = $2
		`, score, item.GUID); err != nil {
			log.Printf("Updating score for item %q: %s", item.GUID, err)
		}
	}
	return rows.Err()
}

func main() {
	log.Printf("Connecting to database...")
	db, err := sql.Open("postgres", "postgresql://feed@10.0.1.1:26257/feed?sslmode=disable")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	log.Printf("Connected")

	var classifierMutex sync.RWMutex
	classifier := newClassifier()

	//trainingDebouncer := newDebouncer(time.Hour)
	//defer trainingDebouncer.stop()

	//go func() {
	//	for range trainingDebouncer.C {
	//		log.Printf("Training...")
	//		if err := train(); err != nil {
	//			panic(err)
	//		}
	//		log.Printf("Done training")

	//		classifierMutex.Lock()
	//		if err := classifier.stop(); err != nil {
	//			log.Printf("Stopping classifier: %s", err)
	//		}
	//		classifier = newClassifier()
	//		classifierMutex.Unlock()

	//		classifierMutex.RLock()
	//		if err := updateScores(classifier, db); err != nil {
	//			log.Printf("Updating scores: %s", err)
	//		}
	//		classifierMutex.RUnlock()
	//	}
	//}()

	go func() {
		t := time.NewTicker(3 * time.Hour)
		defer t.Stop()

		if err := refresh(classifier, db); err != nil {
			log.Printf("Refresh: %s", err)
		}

		if err := updateScores(classifier, db); err != nil {
			log.Printf("Updating scores: %s", err)
		}

		for range t.C {
			log.Printf("Refreshing...")
			classifierMutex.RLock()
			if err := refresh(classifier, db); err != nil {
				log.Printf("Refresh: %s", err)
			}
			classifierMutex.RUnlock()
			log.Printf("Done refreshing")
		}
	}()

	var templ = template.Must(template.ParseFiles("template.html"))

	http.HandleFunc("/click", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			panic(err)
		}

		guid := r.Form.Get("guid")
		link := r.Form.Get("link")

		log.Printf("guid = %q, link = %q", guid, link)

		if _, err := db.Exec(`
			UPDATE item
			SET judgement = TRUE
			WHERE guid = $1
		`, guid); err != nil {
			panic(err)
		}

		//trainingDebouncer.ping()

		http.Redirect(w, r, link, http.StatusMovedPermanently)
	})

	http.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			panic(err)
		}

		// TODO could be more efficiently batched
		for _, guid := range r.Form["guid"] {
			if _, err := db.Exec(`
				UPDATE item
				SET judgement = FALSE
				WHERE guid = $1 AND judgement IS NULL
			`, guid); err != nil {
				panic(err)
			}
		}

		//trainingDebouncer.ping()

		http.Redirect(w, r, "/", http.StatusFound)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(`
			SELECT guid, feed, title, link
			FROM item
			WHERE judgement IS NULL
			ORDER BY score
			LIMIT 3
		`)
		if err != nil {
			panic(err)
		}
		defer rows.Close()

		classifierMutex.RLock()
		defer classifierMutex.RUnlock()

		items := make([]feedItem, 0)
		for rows.Next() {
			var item feedItem

			if err := rows.Scan(&item.GUID, &item.Feed, &item.Title, &item.Link); err != nil {
				panic(err)
			}

			item.Score = classifier.classify(classifiableString(item))

			items = append(items, item)
		}

		if err := rows.Err(); err != nil {
			panic(err)
		}

		if err := templ.ExecuteTemplate(w, "index", struct {
			Items  []feedItem
			Shown  int
			Elided int
		}{
			Items: items,
			Shown: len(items),
		}); err != nil {
			panic(err)
		}
	})

	host := os.Getenv("host")

	log.Printf("Listening on %q", host)
	log.Fatal(http.ListenAndServe(host, nil))
}
