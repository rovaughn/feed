package main

import (
	"database/sql"
	"encoding/json"
	_ "github.com/lib/pq"
	"html/template"
	"log"
	"net/http"
	"time"
)

func main() {
	db, err := sql.Open("postgres", "postgresql://localhost:26257/feed?sslmode=disable")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	if err := train(db); err != nil {
		log.Printf("Training: %s", err)
	}

	var classifier classifier = &zeroClassifier{}

	//if _, err := os.Stat("model.bin"); os.IsNotExist(err) {
	//	if err := train(db); err != nil {
	//		panic(err)
	//	}
	//} else if err != nil {
	//	panic(err)
	//}

	//classifier := newClassifier()

	//go func() {
	//	if err := classifier.serve(); err != nil {
	//		log.Printf("classifier: %s", err)
	//	}
	//}()

	go func() {
		t := time.NewTicker(3 * time.Hour)
		defer t.Stop()

		for range t.C {
			log.Printf("Refreshing...")
			if err := refresh(classifier, db); err != nil {
				log.Printf("Refresh: %s", err)
			}
		}
	}()

	var templ = template.Must(template.ParseFiles("template.html"))

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

		items := make([]feedItem, 0)
		for rows.Next() {
			var item feedItem

			if err := rows.Scan(&item.GUID, &item.Feed, &item.Link, &item.Title); err != nil {
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

	http.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			panic(err)
		}

		var judgements map[string]bool
		if err := json.Unmarshal([]byte(r.PostForm.Get("judgements")), &judgements); err != nil {
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

	log.Printf("Listening on :8000")
	log.Fatal(http.ListenAndServe(":8000", nil))
}
