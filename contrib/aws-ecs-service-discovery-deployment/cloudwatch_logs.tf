resource "aws_cloudwatch_log_group" "gubernator" {
  name = "${var.prefix}/gubernator"

  retention_in_days = 90

  lifecycle {
    ignore_changes = [retention_in_days]
  }
}
