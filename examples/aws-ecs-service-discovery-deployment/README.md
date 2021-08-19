# examples/ecs-service-discovery-deployment

This example deploys Gubernator into AWS ECS with AWS ECS Service Discovery as peer discovery service.

This will set up an ECS Cluster that runs AWS Fargate Spot (which is very cheap to run).

To test this, you'd need to be inside the VPC that this example creates or somehow peer the VPC you're going to use with this example.

Also don't forget to add NS records generated from this to your main Hosted Zone.
e.g. your main hosted zone is `example.com` and you input `dns_namespace` as `gubernator.example.com`, this will create a new Hosted Zone in Route53 with NS record (`gubernator.example.com`) and make sure to add those NS record to `example.com` else when you query `gubernator.example.com` it won't work.
Real gubernator would be accessible in `app.gubernator.example.com`

## Requirements

| Name | Version |
|------|---------|
| <a name="requirement_terraform"></a> [terraform](#requirement\_terraform) | >= 0.12.26 |
| <a name="requirement_aws"></a> [aws](#requirement\_aws) | >= 3.35.0 |

## Providers

| Name | Version |
|------|---------|
| <a name="provider_aws"></a> [aws](#provider\_aws) | 3.54.0 |

## Modules

| Name | Source | Version |
|------|--------|---------|
| <a name="module_app_security_group"></a> [app\_security\_group](#module\_app\_security\_group) | terraform-aws-modules/security-group/aws | ~> 3.0 |
| <a name="module_ecs_cluster"></a> [ecs\_cluster](#module\_ecs\_cluster) | HENNGE/ecs/aws | ~> 2.0 |
| <a name="module_gubernator_service"></a> [gubernator\_service](#module\_gubernator\_service) | HENNGE/ecs/aws//modules/simple/fargate-spot | ~> 2.0 |
| <a name="module_vpc"></a> [vpc](#module\_vpc) | terraform-aws-modules/vpc/aws | ~> 3.0 |

## Resources

| Name | Type |
|------|------|
| [aws_cloudwatch_log_group.gubernator](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/cloudwatch_log_group) | resource |
| [aws_iam_role.ecs_agent_role](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role) | resource |
| [aws_iam_role.ecs_task_role](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role) | resource |
| [aws_iam_role_policy.ecs_agent](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy) | resource |
| [aws_iam_role_policy.ecs_exec_permissions](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy) | resource |
| [aws_iam_role_policy.ecs_task_cloudwatch_access](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy) | resource |
| [aws_service_discovery_public_dns_namespace.namespace](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/service_discovery_public_dns_namespace) | resource |
| [aws_service_discovery_service.gubernator](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/service_discovery_service) | resource |
| [aws_availability_zones.available](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/data-sources/availability_zones) | data source |
| [aws_region.current_region](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/data-sources/region) | data source |

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|:--------:|
| <a name="input_dns_namespace"></a> [dns\_namespace](#input\_dns\_namespace) | The domain name the service should run. Your Gubernator instances will be available at `app.<your inputted fqdn>.` | `string` | n/a | yes |
| <a name="input_gubernator_config"></a> [gubernator\_config](#input\_gubernator\_config) | Map of ECS Configuration for gubernator service. map(cpu, memory) | `any` | <pre>{<br>  "cpu": 256,<br>  "memory": 512<br>}</pre> | no |
| <a name="input_gubernator_debug_mode"></a> [gubernator\_debug\_mode](#input\_gubernator\_debug\_mode) | Enable GUBER\_DEBUG env flag | `bool` | `false` | no |
| <a name="input_gubernator_repository"></a> [gubernator\_repository](#input\_gubernator\_repository) | Gubernator Docker Repository. e.g. thrawn01/gubernator | `string` | n/a | yes |
| <a name="input_gubernator_version"></a> [gubernator\_version](#input\_gubernator\_version) | Gubernator docker tag to use | `string` | n/a | yes |
| <a name="input_prefix"></a> [prefix](#input\_prefix) | Prefix of created resources | `string` | n/a | yes |
| <a name="input_vpc_cidr"></a> [vpc\_cidr](#input\_vpc\_cidr) | IPv4 CIDR Notation for VPC IP range. e.g. 10.3.0.0/16 | `string` | n/a | yes |

## Outputs

No outputs.

