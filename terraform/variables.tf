variable "env" {
  description = "Deployment environment (staging | production)"
  type        = string
}

variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "us-east-2"
}

variable "app_domain" {
  description = "App domain used for image redirects (e.g. lets-roll.org)"
  type        = string
  default     = "lets-roll.org"
}

variable "lambda_zip_path" {
  description = "Path to the compiled Lambda zip"
  type        = string
  default     = "../function.zip"
}

variable "watcher_interval" {
  description = "DB watcher check interval (Go duration string)"
  type        = string
  default     = "1h"
}
