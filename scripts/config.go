package main

import (
	"github.com/BurntSushi/toml"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
)

var sess = session.Must(session.NewSession())

func main() {
	svc := dynamodb.New(sess)

	var config struct {
		Feeds map[string]string `toml:"feeds"`
	}

	if _, err := toml.DecodeFile("config.toml", &config); err != nil {
		panic(err)
	}

	for feed, link := range config.Feeds {
		if _, err := svc.PutItem(&dynamodb.PutItemInput{
			TableName: aws.String("feeds"),
			Item: map[string]*dynamodb.AttributeValue{
				"feed": &dynamodb.AttributeValue{S: aws.String(feed)},
				"link": &dynamodb.AttributeValue{S: aws.String(link)},
			},
		}); err != nil {
			panic(err)
		}
	}
}
