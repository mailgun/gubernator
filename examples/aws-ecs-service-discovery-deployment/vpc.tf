data "aws_availability_zones" "available" {}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 3.0"

  name = "${var.prefix}-vpc"
  cidr = var.vpc_cidr
  azs  = data.aws_availability_zones.available.names

  public_subnets = [for i in range(length(data.aws_availability_zones.available.names)) : cidrsubnet(var.vpc_cidr, 8, i)]

  enable_nat_gateway               = false
  enable_dhcp_options              = true
  enable_dns_hostnames             = true
  enable_dns_support               = true
  dhcp_options_domain_name_servers = ["AmazonProvidedDNS"]

  enable_ipv6                                   = true
  assign_ipv6_address_on_creation               = true
  public_subnet_assign_ipv6_address_on_creation = true
  public_subnet_ipv6_prefixes                   = range(length(data.aws_availability_zones.available.names))
}
