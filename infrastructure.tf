variable "name" {
  type = "string"
}

#variable "domain" {
#  type = "string"
#}

variable "region" {
  type = "string"
}

variable "account_id" {
  type = "string"
}

output "api_url" {
  value = "https://${aws_api_gateway_rest_api.api.id}.execute-api.${var.region}.amazonaws.com"
}

provider "aws" {
  region = "${var.region}"
}

terraform {
  backend "s3" {
    bucket = "alec-personal"
    key    = "feed-terraform-state"
  }
}
