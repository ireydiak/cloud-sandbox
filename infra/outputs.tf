output "alb_dns_name" { value = aws_lb.main.dns_name }
output "ecr_repository_url" { value = aws_ecr_repository.server.repository_url }
output "ecs_cluster_name" { value = aws_ecs_cluster.main.name }
output "ecs_service_name" { value = aws_ecs_service.server.name }
output "ws_api_url" { value = aws_apigatewayv2_stage.lab.invoke_url }
output "push_function_name" { value = aws_lambda_function.push.function_name }
output "region" { value = var.region }
