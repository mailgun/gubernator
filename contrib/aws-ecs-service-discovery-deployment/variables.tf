variable "prefix" {
  type        = string
  description = "Prefix of created resources"
}

# VPC Options
variable "vpc_cidr" {
  description = "IPv4 CIDR Notation for VPC IP range. e.g. 10.3.0.0/16"
  type        = string
}

variable "dns_namespace" {
  description = "The domain name the service should run. Your Gubernator instances will be available at `app.<your inputted fqdn>."
  type        = string
}

variable "gubernator_repository" {
  type        = string
  description = "Gubernator Docker Repository. e.g. ghcr.io/mailgun/gubernator"
}

variable "gubernator_version" {
  type        = string
  description = "Gubernator docker tag to use"
}

variable "gubernator_config" {
  description = "Map of ECS Configuration for gubernator service. map(cpu, memory)"
  default     = { cpu = 256, memory = 512 }
  type        = any
}

variable "gubernator_debug_mode" {
  description = "Enable GUBER_DEBUG env flag"
  default     = false
  type        = bool
}
