package main

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/mmcdole/gofeed"
	"log"
	"math/rand"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type item struct {
	guid  string
	link  string
	title string
}

func scrapeFeed(link string) ([]item, error) {
	fp := gofeed.NewParser()
	result, err := fp.ParseURL(link)
	if err != nil {
		return nil, err
	}

	items := make([]item, 0)
	for _, i := range result.Items {
		if i.GUID == "" {
			i.GUID = i.Link
		}

		items = append(items, item{
			guid:  i.GUID,
			link:  i.Link,
			title: i.Title,
		})
	}

	return items, nil
}

func refresh() {
	svc := dynamodb.New(sess)

	log.Printf("Refreshing...")
	result, err := svc.Scan(&dynamodb.ScanInput{
		TableName:            aws.String("feeds"),
		ProjectionExpression: aws.String("feed, link"),
	})
	if err != nil {
		log.Printf("Scanning feeds: %s", err)
		return
	}

	var group sync.WaitGroup
	for _, feedItem := range result.Items {
		feedItem := feedItem
		group.Add(1)
		go func() {
			defer group.Done()
			time.Sleep(time.Duration(rand.Intn(2000)) * time.Millisecond)

			feed := *feedItem["feed"].S
			link := *feedItem["link"].S

			parsedLink, err := url.Parse(link)
			if err != nil {
				log.Printf("Parsing feed link %q: %s", link, err)
				return
			}

			var items []item

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
				if _, err := svc.PutItem(&dynamodb.PutItemInput{
					TableName:           aws.String("items"),
					ConditionExpression: aws.String("attribute_not_exists(guid)"),
					Item: map[string]*dynamodb.AttributeValue{
						"label": &dynamodb.AttributeValue{S: aws.String("none")},
						"feed":  &dynamodb.AttributeValue{S: aws.String(feed)},
						"guid":  &dynamodb.AttributeValue{S: aws.String(item.guid)},
						"title": &dynamodb.AttributeValue{S: aws.String(item.title)},
						"link":  &dynamodb.AttributeValue{S: aws.String(item.link)},
					},
				}); err != nil {
					if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == dynamodb.ErrCodeConditionalCheckFailedException {
						// don't log
					} else {
						log.Printf("Inserting item %q: %s", item.guid, err)
					}
				}
			}
		}()
	}

	group.Wait()
	log.Printf("Done")
}
