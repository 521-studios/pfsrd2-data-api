variable "env" {
  description = "Deployment environment (staging | production)"
  type        = string
}

variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "us-east-2"
}

variable "infra_state_bucket" {
  description = "S3 bucket holding the shared infra Terraform state"
  type        = string
}

variable "image_domain" {
  description = "CloudFront domain for image serving"
  type        = string
  default     = "images.pfsrd2.521studios.com"
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
