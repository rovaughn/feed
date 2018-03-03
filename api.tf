resource "aws_iam_role" "api_function" {
  name = "${var.name}-api-function-role"

  # This policy determines who/what can assume this role.  This means
  # "lambda" can call our function.
  assume_role_policy = <<EOF
{
  "Version": "2012-10-17",
  "Statement": [{
    "Action": "sts:AssumeRole",
    "Principal": {
      "Service": "lambda.amazonaws.com"
    },
    "Effect": "Allow",
    "Sid": ""
  }]
}
EOF
}

resource "aws_iam_role_policy" "api_function_policy" {
  name = "${var.name}-api-function-policy"
  role = "${aws_iam_role.api_function.id}"

	# TODO constrain dynamodb action/resource
  policy = <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Action": "logs:*",
      "Effect": "Allow",
      "Resource": "*"
    },
		{
			"Effect": "Allow",
			"Action": [
				"ec2:CreateNetworkInterface",
				"ec2:DescribeNetworkInterfaces",
				"ec2:DeleteNetworkInterface"
			],
			"Resource": "*"
		},
		{
			"Effect": "Allow",
			"Action": [
					"s3:*"
			],
			"Resource": "*"
		},
		{
			"Effect": "Allow",
			"Action": [
					"dynamodb:*"
			],
			"Resource": "*"
		}
  ]
}
EOF
}

resource "aws_security_group" "lambda" {
  name        = "${var.name}-api"
  description = "Allows lambda to access resources"
  #vpc_id      = "${aws_vpc.default.id}"

  egress {
    from_port   = 3306
    to_port     = 3306
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_lambda_function" "api" {
  function_name    = "${var.name}-api"
  filename         = "./api.zip"
  source_code_hash = "${base64sha256(file("api.zip"))}"
  role             = "${aws_iam_role.api_function.arn}"
  handler          = "main"
  runtime          = "go1.x"
	timeout          = 16
  memory_size      = 1024

  environment {
    variables = {
      environment = "lambda"
    }
  }
}

resource "aws_lambda_permission" "api_root" {
  statement_id  = "AllowExecutionFromAPIGateway"
  action        = "lambda:InvokeFunction"
  function_name = "${aws_lambda_function.api.arn}"
  principal     = "apigateway.amazonaws.com"
  source_arn    = "arn:aws:execute-api:${var.region}:${var.account_id}:${aws_api_gateway_rest_api.api.id}/*/*"
}

resource "aws_api_gateway_rest_api" "api" {
  name = "${var.name}-api"
}

resource "aws_api_gateway_method" "api_root_get" {
  rest_api_id   = "${aws_api_gateway_rest_api.api.id}"
  resource_id   = "${aws_api_gateway_rest_api.api.root_resource_id}"
  http_method   = "GET"
  authorization = "NONE"
}

resource "aws_api_gateway_integration" "api_root_get" {
  rest_api_id             = "${aws_api_gateway_rest_api.api.id}"
  resource_id             = "${aws_api_gateway_rest_api.api.root_resource_id}"
  http_method             = "GET"
  integration_http_method = "POST"
  type                    = "AWS_PROXY"
  uri                     = "arn:aws:apigateway:${var.region}:lambda:path/2015-03-31/functions/${aws_lambda_function.api.arn}/invocations"
}

resource "aws_api_gateway_method" "api_root_post" {
  rest_api_id   = "${aws_api_gateway_rest_api.api.id}"
  resource_id   = "${aws_api_gateway_rest_api.api.root_resource_id}"
  http_method   = "POST"
  authorization = "NONE"
}

resource "aws_api_gateway_integration" "api_root_post" {
  rest_api_id             = "${aws_api_gateway_rest_api.api.id}"
  resource_id             = "${aws_api_gateway_rest_api.api.root_resource_id}"
  http_method             = "POST"
  integration_http_method = "POST"
  type                    = "AWS_PROXY"
  uri                     = "arn:aws:apigateway:${var.region}:lambda:path/2015-03-31/functions/${aws_lambda_function.api.arn}/invocations"
}

resource "aws_api_gateway_deployment" "api_deployment" {
  depends_on = [
    "aws_api_gateway_integration.api_root_get",
    "aws_api_gateway_integration.api_root_post"
  ]

  rest_api_id = "${aws_api_gateway_rest_api.api.id}"
  stage_name  = "test"
}
