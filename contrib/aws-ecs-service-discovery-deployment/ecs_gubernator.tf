data "aws_region" "current_region" {}

module "gubernator_service" {
  source  = "HENNGE/ecs/aws//modules/simple/fargate-spot"
  version = "~> 2.0"

  ignore_desired_count_changes = true

  name          = "${var.prefix}-gubernator-service"
  cluster       = module.ecs_cluster.name
  cpu           = var.gubernator_config["cpu"]
  memory        = var.gubernator_config["memory"]
  desired_count = lookup(var.gubernator_config, "desired_count", 1)

  iam_daemon_role = aws_iam_role.ecs_agent_role.arn
  iam_task_role   = aws_iam_role.ecs_task_role.arn

  security_groups  = [module.app_security_group.this_security_group_id]
  vpc_subnets      = module.vpc.public_subnets
  assign_public_ip = true

  service_registry = {
    registry_arn = aws_service_discovery_service.gubernator.arn
  }

  container_definitions = jsonencode(
    [
      {
        name      = "gubernator",
        image     = "${var.gubernator_repository}:${var.gubernator_version}",
        cpu       = var.gubernator_config["cpu"],
        memory    = var.gubernator_config["memory"],
        essential = true,
        linuxParameters = {
          initProcessEnabled = true
        },
        portMappings = [
          { containerPort = 80 },
          { containerPort = 81 },
        ],
        logConfiguration = {
          logDriver = "awslogs",
          options = {
            awslogs-group         = aws_cloudwatch_log_group.gubernator.name,
            awslogs-region        = data.aws_region.current_region.name,
            awslogs-stream-prefix = var.gubernator_version
          }
        },
        environment = [
          for env_key, env_value in local.gubernator_env_vars :
          {
            name  = env_key,
            value = env_value
          }
        ],
      }
    ]
  )

  enable_deployment_circuit_breaker_with_rollback = true
  wait_for_steady_state                           = true
  enable_execute_command                          = true

  propagate_tags          = "SERVICE"
  enable_ecs_managed_tags = true
}
