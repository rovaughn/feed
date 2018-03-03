resource "aws_dynamodb_table" "items" {
	name = "items"
	read_capacity = 1
	write_capacity = 10
	hash_key = "guid"

	attribute {
		name = "guid"
		type = "S"
	}
}

resource "aws_dynamodb_table" "feeds" {
	name = "feeds"
	read_capacity = 1
	write_capacity = 1
	hash_key = "feed"

	attribute {
		name = "feed"
		type = "S"
	}
}
