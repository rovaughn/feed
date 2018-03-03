package main

import (
	"database/sql"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	_ "github.com/mattn/go-sqlite3"
	"log"
)

var sess = session.Must(session.NewSession())

func main() {
	db, err := sql.Open("sqlite3", "db.sqlite3")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item`).Scan(&count); err != nil {
		panic(err)
	}

	svc := dynamodb.New(sess)

	var completed int
	rows, err := db.Query(`
		SELECT feed, guid, title, link, judgement
		FROM item
	`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	for rows.Next() {
		var feed, guid, title, link string
		var judgement *bool
		if err := rows.Scan(&feed, &guid, &title, &link, &judgement); err != nil {
			panic(err)
		}

		var label string
		if judgement == nil {
			label = "none"
		} else if *judgement {
			label = "1"
		} else {
			label = "0"
		}

		if _, err := svc.PutItem(&dynamodb.PutItemInput{
			TableName: aws.String("items"),
			Item: map[string]*dynamodb.AttributeValue{
				"label": &dynamodb.AttributeValue{S: aws.String(label)},
				"feed":  &dynamodb.AttributeValue{S: aws.String(feed)},
				"guid":  &dynamodb.AttributeValue{S: aws.String(guid)},
				"title": &dynamodb.AttributeValue{S: aws.String(title)},
				"link":  &dynamodb.AttributeValue{S: aws.String(link)},
			},
		}); err != nil {
			panic(err)
		}

		completed++
		log.Printf("Finished %d/%d", completed, count)
	}
}
