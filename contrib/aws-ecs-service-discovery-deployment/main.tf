locals {
  service_namespace            = var.dns_namespace
  gubernator_service_discovery = "app"
  gubernator_service_host      = "${local.gubernator_service_discovery}.${local.service_namespace}"
  gubernator_env_vars = {
    GUBER_HTTP_ADDRESS        = "0.0.0.0:80"
    GUBER_PEER_DISCOVERY_TYPE = "dns"
    GUBER_DNS_FQDN            = local.gubernator_service_host
    GUBER_DEBUG               = var.gubernator_debug_mode ? "true" : "false"
  }
}