resource "aws_ecr_repository" "server" {
  name         = local.name
  force_delete = true
}
