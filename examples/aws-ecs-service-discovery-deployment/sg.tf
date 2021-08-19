module "app_security_group" {
  source  = "terraform-aws-modules/security-group/aws"
  version = "~> 3.0"

  name   = "${var.prefix}-app-sg"
  vpc_id = module.vpc.vpc_id

  # Ingress from this SG
  ingress_with_self = [
    {
      rule = "all-all"
    },
  ]

  ingress_ipv6_cidr_blocks = [module.vpc.vpc_ipv6_cidr_block]
  ingress_rules            = ["all-all"]

  # Allow all egress
  egress_cidr_blocks      = ["0.0.0.0/0"]
  egress_ipv6_cidr_blocks = ["::/0"]
  egress_rules            = ["all-all"]
}
