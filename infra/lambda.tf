# The Makefile builds build/lambda-*/bootstrap before any plan/apply/destroy.
data "archive_file" "ws" {
  type        = "zip"
  source_file = "${path.module}/../build/lambda-ws/bootstrap"
  output_path = "${path.module}/../build/lambda-ws.zip"
}

data "archive_file" "push" {
  type        = "zip"
  source_file = "${path.module}/../build/lambda-push/bootstrap"
  output_path = "${path.module}/../build/lambda-push.zip"
}

data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "lambda_ws" {
  name               = "${local.name}-lambda-ws"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_iam_role" "lambda_push" {
  name               = "${local.name}-lambda-push"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

data "aws_caller_identity" "me" {}

locals {
  manage_connections_arn = "arn:aws:execute-api:${var.region}:${data.aws_caller_identity.me.account_id}:${aws_apigatewayv2_api.ws.id}/*"
}

# Scoped to the log group tofu owns, so Lambda never auto-creates its own.
data "aws_iam_policy_document" "lambda_ws" {
  statement {
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.lambda_ws.arn}:*"]
  }
  statement {
    actions   = ["dynamodb:PutItem", "dynamodb:GetItem", "dynamodb:DeleteItem"]
    resources = [aws_dynamodb_table.connections.arn]
  }
  statement {
    actions   = ["execute-api:ManageConnections"]
    resources = [local.manage_connections_arn]
  }
}

data "aws_iam_policy_document" "lambda_push" {
  statement {
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.lambda_push.arn}:*"]
  }
  statement {
    actions   = ["execute-api:ManageConnections"]
    resources = [local.manage_connections_arn]
  }
}

resource "aws_iam_role_policy" "lambda_ws" {
  role   = aws_iam_role.lambda_ws.id
  policy = data.aws_iam_policy_document.lambda_ws.json
}

resource "aws_iam_role_policy" "lambda_push" {
  role   = aws_iam_role.lambda_push.id
  policy = data.aws_iam_policy_document.lambda_push.json
}

resource "aws_lambda_function" "ws" {
  function_name    = "${local.name}-ws"
  role             = aws_iam_role.lambda_ws.arn
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  handler          = "bootstrap"
  filename         = data.archive_file.ws.output_path
  source_code_hash = data.archive_file.ws.output_base64sha256
  memory_size      = 128
  timeout          = 10

  environment {
    variables = { TABLE_NAME = aws_dynamodb_table.connections.name }
  }

  logging_config {
    log_format = "Text"
    log_group  = aws_cloudwatch_log_group.lambda_ws.name
  }

  depends_on = [aws_cloudwatch_log_group.lambda_ws, aws_iam_role_policy.lambda_ws]
}

resource "aws_lambda_function" "push" {
  function_name    = "${local.name}-push"
  role             = aws_iam_role.lambda_push.arn
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  handler          = "bootstrap"
  filename         = data.archive_file.push.output_path
  source_code_hash = data.archive_file.push.output_base64sha256
  memory_size      = 128
  timeout          = 10

  environment {
    variables = { API_ENDPOINT = "https://${aws_apigatewayv2_api.ws.id}.execute-api.${var.region}.amazonaws.com/${aws_apigatewayv2_stage.lab.name}" }
  }

  logging_config {
    log_format = "Text"
    log_group  = aws_cloudwatch_log_group.lambda_push.name
  }

  depends_on = [aws_cloudwatch_log_group.lambda_push, aws_iam_role_policy.lambda_push]
}
