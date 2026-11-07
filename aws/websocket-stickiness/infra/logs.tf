# Every log group is owned by tofu so destroy removes it (retention: 1 day).
resource "aws_cloudwatch_log_group" "ecs" {
  name              = "/ecs/${local.name}"
  retention_in_days = 1
}

resource "aws_cloudwatch_log_group" "lambda_ws" {
  name              = "/aws/lambda/${local.name}-ws"
  retention_in_days = 1
}

resource "aws_cloudwatch_log_group" "lambda_push" {
  name              = "/aws/lambda/${local.name}-push"
  retention_in_days = 1
}
