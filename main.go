package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/mmcdole/gofeed"
	"html/template"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"
)

var sess = session.Must(session.NewSession())

var c = func() *classifier {
	c := newClassifier()
	go func() {
		if err := c.serve(); err != nil {
			log.Printf("classifier: %s", err)
		}
	}()
	return c
}()

func refresh() {
	svc := dynamodb.New(sess)
	fp := gofeed.NewParser()

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
	for _, item := range result.Items {
		item := item
		group.Add(1)
		go func() {
			defer group.Done()

			feed := *item["feed"].S
			link := *item["link"].S

			time.Sleep(time.Duration(rand.Intn(2000)) * time.Millisecond)
			downloaded, err := fp.ParseURL(link)
			if err != nil {
				log.Printf("Parsing feed for %q (%q): %s", feed, link, err)
				return
			}

			for _, item := range downloaded.Items {
				if item.GUID == "" {
					item.GUID = item.Link
				}

				if _, err := svc.PutItem(&dynamodb.PutItemInput{
					TableName:           aws.String("items"),
					ConditionExpression: aws.String("attribute_not_exists(guid)"),
					Item: map[string]*dynamodb.AttributeValue{
						"label": &dynamodb.AttributeValue{S: aws.String("none")},
						"feed":  &dynamodb.AttributeValue{S: aws.String(feed)},
						"guid":  &dynamodb.AttributeValue{S: aws.String(item.GUID)},
						"title": &dynamodb.AttributeValue{S: aws.String(item.Title)},
						"link":  &dynamodb.AttributeValue{S: aws.String(item.Link)},
					},
				}); err != nil {
					if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == dynamodb.ErrCodeConditionalCheckFailedException {
						// don't log
					} else {
						log.Printf("Inserting item: %s", err)
					}
				}
			}
		}()
	}

	group.Wait()
	log.Printf("Done")
}

var templ = template.Must(template.ParseFiles("template.html"))

func handler(req events.APIGatewayProxyRequest) (*events.APIGatewayProxyResponse, error) {
	log.Printf("%s %s", req.HTTPMethod, req.Path)
	if req.HTTPMethod == "OPTIONS" {
		return &events.APIGatewayProxyResponse{
			StatusCode: http.StatusOK,
			Headers: map[string]string{
				"Access-Control-Allow-Headers": "content-type",
				"Access-Control-Allow-Origin":  "*",
			},
		}, nil
	} else if req.HTTPMethod == "GET" && req.Path == "/" {
		//refresh()

		type feedItem struct {
			GUID  string
			Feed  string
			Link  string
			Title string
			Score float64
		}

		items := make([]feedItem, 0)
		svc := dynamodb.New(sess)
		result, err := svc.Scan(&dynamodb.ScanInput{
			TableName:            aws.String("items"),
			ProjectionExpression: aws.String("feed, guid, title, link"),
			FilterExpression:     aws.String(`label = :none`),
			ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
				":none": &dynamodb.AttributeValue{S: aws.String("none")},
			},
		})
		if err != nil {
			return nil, err
		}
		for _, item := range result.Items {
			items = append(items, feedItem{
				GUID:  *item["guid"].S,
				Link:  *item["link"].S,
				Feed:  *item["feed"].S,
				Title: *item["title"].S,
				Score: c.classify(classifiableString(item)),
			})
		}

		sort.Slice(items, func(i, j int) bool {
			return items[i].Score > items[j].Score
		})

		const maxItems = 3
		shownItems := items
		if len(shownItems) > maxItems {
			shownItems = shownItems[:maxItems]
		}

		var buf bytes.Buffer
		if err := templ.ExecuteTemplate(&buf, "index", struct {
			Items  []feedItem
			Shown  int
			Elided int
		}{
			Items:  shownItems,
			Shown:  len(shownItems),
			Elided: len(items) - len(shownItems),
		}); err != nil {
			return nil, err
		}

		return &events.APIGatewayProxyResponse{
			StatusCode: http.StatusOK,
			Headers: map[string]string{
				"Access-Control-Allow-Origin":  "*",
				"Access-Control-Allow-Headers": "content-type",
				"Content-Type":                 "text/html",
			},
			Body: buf.String(),
		}, nil
	} else if req.HTTPMethod == "POST" && req.Path == "/submit" {
		values, err := url.ParseQuery(req.Body)
		if err != nil {
			return nil, err
		}

		var judgements map[string]bool
		if err := json.Unmarshal([]byte(values.Get("judgements")), &judgements); err != nil {
			return nil, err
		}

		svc := dynamodb.New(sess)
		for guid, judgement := range judgements {
			var label string
			if judgement {
				label = "1"
			} else {
				label = "0"
			}

			if _, err := svc.UpdateItem(&dynamodb.UpdateItemInput{
				TableName:           aws.String("items"),
				ConditionExpression: aws.String("guid = :guid"),
				ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
					":guid":  &dynamodb.AttributeValue{S: aws.String(guid)},
					":label": &dynamodb.AttributeValue{S: aws.String(label)},
				},
				UpdateExpression: aws.String("SET label = :label"),
			}); err != nil {
				return nil, err
			}
		}

		return &events.APIGatewayProxyResponse{
			StatusCode: http.StatusFound,
			Headers: map[string]string{
				"Location": "/",
			},
		}, nil
	}

	return &events.APIGatewayProxyResponse{
		StatusCode: http.StatusNotFound,
		Body:       "Invalid method or path",
	}, nil
}

func main() {
	log.Printf("Starting lambda function up")

	switch os.Getenv("environment") {
	case "local":
		var addr string
		flag.StringVar(&addr, "addr", ":80", "Address to listen on")
		flag.Parse()

		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			var req events.APIGatewayProxyRequest
			req.HTTPMethod = r.Method
			req.Path = r.URL.Path
			body, err := ioutil.ReadAll(r.Body)
			if err != nil {
				panic(err)
			}
			req.Body = string(body)

			res, err := handler(req)
			if err != nil {
				panic(err)
			}

			for key, value := range res.Headers {
				w.Header().Add(key, value)
			}

			w.WriteHeader(res.StatusCode)

			if _, err := w.Write([]byte(res.Body)); err != nil {
				panic(err)
			}
		})

		log.Println("Serving graphql on", addr)
		log.Fatal(http.ListenAndServe(addr, nil))
	case "lambda":
		lambda.Start(handler)
	default:
		panic("Unknown environment")
	}
}
