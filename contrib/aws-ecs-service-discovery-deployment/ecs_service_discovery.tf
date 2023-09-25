resource "aws_service_discovery_public_dns_namespace" "namespace" {
  name = local.service_namespace
}

resource "aws_service_discovery_service" "gubernator" {
  name = local.gubernator_service_discovery
  dns_config {
    namespace_id = aws_service_discovery_public_dns_namespace.namespace.id
    dns_records {
      ttl  = 10
      type = "A"
    }
    routing_policy = "MULTIVALUE"
  }
  health_check_custom_config {
    failure_threshold = 1
  }
}
