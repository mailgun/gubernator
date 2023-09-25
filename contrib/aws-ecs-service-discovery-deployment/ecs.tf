module "ecs_cluster" {
  source  = "HENNGE/ecs/aws"
  version = "~> 2.0"

  name = "${var.prefix}-cluster"

  enable_container_insights = true
}
