#!/usr/bin/env bash
# Fails (and lists leftovers) if anything from the ws-lab stack still exists.
# The tagging API lags after deletion, so retry for up to 5 minutes.
set -uo pipefail
REGION="${AWS_REGION:-us-east-1}"
P=ws-lab
deadline=$((SECONDS + 300))

# The tagging API lags and AWS keeps deleted ECS objects visible as INACTIVE.
# Print the ARN only if the resource is really still there (unknown types are
# treated as present, so we never hide a leftover we cannot classify).
live_arn() {
  local arn="$1" id="${1##*/}" out
  case "$arn" in
    arn:aws:ecs:*:cluster/*)
      out=$(aws ecs describe-clusters --clusters "$arn" --region "$REGION" --query 'clusters[].status' --output text)
      [ -n "$out" ] && [ "$out" != INACTIVE ] && echo "$arn" ;;
    arn:aws:ecs:*:service/*)
      local cluster; cluster=$(cut -d/ -f2 <<<"${arn#*:service/}")
      out=$(aws ecs describe-services --cluster "$cluster" --services "$arn" --region "$REGION" --query 'services[].status' --output text 2>/dev/null)
      [ -n "$out" ] && [ "$out" != INACTIVE ] && echo "$arn" ;;
    arn:aws:ecs:*:task-definition/*)
      out=$(aws ecs describe-task-definition --task-definition "$arn" --region "$REGION" --query 'taskDefinition.status' --output text 2>/dev/null)
      [ "$out" = ACTIVE ] && echo "$arn" ;;
    arn:aws:ec2:*:subnet/*)         aws ec2 describe-subnets --subnet-ids "$id" --region "$REGION" >/dev/null 2>&1 && echo "$arn" ;;
    arn:aws:ec2:*:vpc/*)            aws ec2 describe-vpcs --vpc-ids "$id" --region "$REGION" >/dev/null 2>&1 && echo "$arn" ;;
    arn:aws:ec2:*:security-group/*) aws ec2 describe-security-groups --group-ids "$id" --region "$REGION" >/dev/null 2>&1 && echo "$arn" ;;
    arn:aws:ec2:*:internet-gateway/*) aws ec2 describe-internet-gateways --internet-gateway-ids "$id" --region "$REGION" >/dev/null 2>&1 && echo "$arn" ;;
    arn:aws:ec2:*:route-table/*)    aws ec2 describe-route-tables --route-table-ids "$id" --region "$REGION" >/dev/null 2>&1 && echo "$arn" ;;
    *) echo "$arn" ;;
  esac
}

check() {
  local found=""
  add() { [ -n "$2" ] && found+="$1: $2"$'\n'; }

  local tagged live=""
  tagged=$(aws resourcegroupstaggingapi get-resources --tag-filters Key=Project,Values=$P \
      --region "$REGION" --query 'ResourceTagMappingList[].ResourceARN' --output text 2>&1 | tr '\t' '\n')
  for arn in $tagged; do live+="$(live_arn "$arn")"$'\n'; done
  add "tagged resources" "$(echo "$live" | sed '/^$/d')"
  add "log groups" "$(aws logs describe-log-groups --log-group-name-prefix /aws/lambda/$P --region "$REGION" \
      --query 'logGroups[].logGroupName' --output text | tr '\t' '\n')"
  add "log groups" "$(aws logs describe-log-groups --log-group-name-prefix /ecs/$P --region "$REGION" \
      --query 'logGroups[].logGroupName' --output text | tr '\t' '\n')"
  add "ECR repos" "$(aws ecr describe-repositories --region "$REGION" \
      --query "repositories[?starts_with(repositoryName,'$P')].repositoryName" --output text 2>/dev/null | tr '\t' '\n')"
  add "ECS clusters" "$(aws ecs list-clusters --region "$REGION" --query "clusterArns[?contains(@,'/$P')]" --output text | tr '\t' '\n')"
  add "load balancers" "$(aws elbv2 describe-load-balancers --region "$REGION" \
      --query "LoadBalancers[?starts_with(LoadBalancerName,'$P')].LoadBalancerArn" --output text 2>/dev/null | tr '\t' '\n')"
  add "API Gateway APIs" "$(aws apigatewayv2 get-apis --region "$REGION" \
      --query "Items[?starts_with(Name,'$P')].[Name,ApiId]" --output text | tr '\t' ' ')"
  add "DynamoDB tables" "$(aws dynamodb list-tables --region "$REGION" \
      --query "TableNames[?starts_with(@,'$P')]" --output text | tr '\t' '\n')"
  add "Lambda functions" "$(aws lambda list-functions --region "$REGION" \
      --query "Functions[?starts_with(FunctionName,'$P')].FunctionName" --output text | tr '\t' '\n')"

  FOUND="$found"
  [ -z "$found" ]
}

while true; do
  if check; then echo "verify-clean: nothing left behind."; exit 0; fi
  if [ $SECONDS -ge $deadline ]; then
    echo "verify-clean: LEFTOVERS FOUND after 5 minutes:" >&2
    echo "$FOUND" >&2
    exit 1
  fi
  echo "verify-clean: still seeing resources, retrying in 15s..." >&2
  echo "$FOUND" | sed 's/^/    /' >&2
  sleep 15
done
