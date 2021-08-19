resource "aws_iam_role" "ecs_task_role" {
  name = "${var.prefix}-ecs-task-role"

  assume_role_policy = jsonencode(
    {
      Version = "2008-10-17",
      Statement = [
        {
          Sid    = "",
          Effect = "Allow",
          Principal = {
            Service = "ecs-tasks.amazonaws.com"
          },
          Action = "sts:AssumeRole"
        }
      ]
    }
  )
}

resource "aws_iam_role" "ecs_agent_role" {
  name = "${var.prefix}-ecs-agent-role"

  assume_role_policy = jsonencode(
    {
      Version = "2008-10-17",
      Statement = [
        {
          Sid    = "",
          Effect = "Allow",
          Principal = {
            Service = "ecs-tasks.amazonaws.com"
          },
          Action = "sts:AssumeRole"
        }
      ]
    }
  )
}

resource "aws_iam_role_policy" "ecs_task_cloudwatch_access" {
  name = "${var.prefix}-ecs-task-cloudwatch-access"
  role = aws_iam_role.ecs_task_role.id

  policy = jsonencode(
    {
      Version = "2012-10-17",
      Statement = [
        {
          Effect = "Allow",
          Action = [
            "sts:AssumeRole",
            "logs:CreateLogStream",
            "logs:PutLogEvents",
            "logs:DescribeLogStreams",
            "logs:GetLogEvents",
            "cloudwatch:PutMetricData"
          ],
          Resource = "*"
        }
      ]
    }
  )
}

resource "aws_iam_role_policy" "ecs_exec_permissions" {
  name = "${var.prefix}-ecs-exec-permissions"
  role = aws_iam_role.ecs_task_role.id
  policy = jsonencode(
    {
      Version = "2012-10-17",
      Statement = [
        {
          Effect = "Allow",
          Action = [
            "ssmmessages:CreateControlChannel",
            "ssmmessages:CreateDataChannel",
            "ssmmessages:OpenControlChannel",
            "ssmmessages:OpenDataChannel"
          ],
          Resource = "*"
        }
      ]
    }
  )
}

resource "aws_iam_role_policy" "ecs_agent" {
  name = "${var.prefix}-ecs-agent"
  role = aws_iam_role.ecs_agent_role.id

  policy = jsonencode(
    {
      Version = "2012-10-17",
      Statement = [
        {
          Effect = "Allow",
          Action = [
            "logs:CreateLogStream",
            "logs:PutLogEvents"
          ],
          Resource = "*"
        }
      ]
    }
  )
}
