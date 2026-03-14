terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
  backend "s3" {
    # bucket and key passed via -backend-config at init time (see deploy.yml)
    region = "us-east-2"
  }
}

provider "aws" {
  region = var.aws_region
}

provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"
}

locals {
  name        = "pfsrd2-data-api"
  bucket_name = "521studios-${var.env}-pfsrd2-data"
  tags = {
    Project     = "pfsrd2-data-api"
    Environment = var.env
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# Route53 zone — looked up by name, not via infra remote state
# ---------------------------------------------------------------------------

data "aws_route53_zone" "primary" {
  name         = var.route53_zone_name
  private_zone = false
}

# ---------------------------------------------------------------------------
# S3 — owned by this service
# ---------------------------------------------------------------------------

resource "aws_s3_bucket" "data" {
  bucket = local.bucket_name
  tags   = local.tags
}

resource "aws_s3_bucket_versioning" "data" {
  bucket = aws_s3_bucket.data.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "data" {
  bucket                  = aws_s3_bucket.data.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "data" {
  bucket = aws_s3_bucket.data.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# ---------------------------------------------------------------------------
# IAM policies — owned by this service
# ---------------------------------------------------------------------------

resource "aws_iam_policy" "lambda_s3" {
  name        = "521studios-${var.env}-pfsrd2-lambda-s3"
  description = "pfsrd2-data-api Lambda: read db/ and json/ from pfsrd2-data bucket"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "LambdaReadAccess"
        Effect = "Allow"
        Action = ["s3:GetObject", "s3:HeadObject"]
        Resource = [
          "${aws_s3_bucket.data.arn}/db/*",
          "${aws_s3_bucket.data.arn}/json/*",
        ]
      },
    ]
  })

  tags = local.tags
}

resource "aws_iam_policy" "indexer_s3" {
  name        = "521studios-${var.env}-pfsrd2-indexer-s3"
  description = "pf2_build_index: read/write to pfsrd2-data bucket"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "IndexerObjectAccess"
        Effect = "Allow"
        Action = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:HeadObject"]
        Resource = "${aws_s3_bucket.data.arn}/*"
      },
      {
        Sid    = "IndexerBucketAccess"
        Effect = "Allow"
        Action = ["s3:ListBucket", "s3:GetBucketLocation"]
        Resource = aws_s3_bucket.data.arn
      },
    ]
  })

  tags = local.tags
}

# ---------------------------------------------------------------------------
# IAM: Lambda execution role
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "lambda" {
  name               = "${local.name}-${var.env}-lambda"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
  tags               = local.tags
}

resource "aws_iam_role_policy_attachment" "lambda_basic" {
  role       = aws_iam_role.lambda.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_iam_role_policy_attachment" "lambda_s3" {
  role       = aws_iam_role.lambda.name
  policy_arn = aws_iam_policy.lambda_s3.arn
}

# ---------------------------------------------------------------------------
# Lambda function
# ---------------------------------------------------------------------------

resource "aws_lambda_function" "api" {
  function_name    = "${local.name}-${var.env}"
  role             = aws_iam_role.lambda.arn
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  memory_size      = 512
  timeout          = 30

  environment {
    variables = {
      BUCKET_NAME      = aws_s3_bucket.data.id
      ENV              = var.env
      IMAGE_DOMAIN     = var.image_domain
      WATCHER_INTERVAL = var.watcher_interval
    }
  }

  tags = local.tags
}

resource "aws_cloudwatch_log_group" "api" {
  name              = "/aws/lambda/${aws_lambda_function.api.function_name}"
  retention_in_days = 14
  tags              = local.tags
}

# ---------------------------------------------------------------------------
# Lambda Function URL — CloudFront uses this as the API origin
# ---------------------------------------------------------------------------

resource "aws_lambda_function_url" "api" {
  function_name      = aws_lambda_function.api.function_name
  authorization_type = "NONE"
}

# Terraform doesn't auto-create the resource policy for NONE auth (unlike the Console).
# This explicitly grants public invoke access to the Function URL.
resource "aws_lambda_permission" "public_url" {
  statement_id           = "FunctionURLAllowPublicAccess"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.api.function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}

# ---------------------------------------------------------------------------
# CloudFront for images
# ---------------------------------------------------------------------------

resource "aws_cloudfront_origin_access_control" "images" {
  name                              = "${local.name}-${var.env}-images"
  description                       = "OAC for pfsrd2 images"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

resource "aws_cloudfront_distribution" "images" {
  enabled = true
  comment = "pfsrd2 images - ${var.env}"
  aliases = [var.image_domain]
  tags    = local.tags

  origin {
    domain_name              = aws_s3_bucket.data.bucket_regional_domain_name
    origin_id                = "s3-images"
    origin_path              = "/images"
    origin_access_control_id = aws_cloudfront_origin_access_control.images.id
  }

  default_cache_behavior {
    target_origin_id       = "s3-images"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    compress               = true

    forwarded_values {
      query_string = false
      cookies { forward = "none" }
    }

    min_ttl     = 0
    default_ttl = 86400   # 1 day
    max_ttl     = 604800  # 7 days
  }

  restrictions {
    geo_restriction { restriction_type = "none" }
  }

  viewer_certificate {
    acm_certificate_arn      = aws_acm_certificate_validation.images.certificate_arn
    ssl_support_method       = "sni-only"
    minimum_protocol_version = "TLSv1.2_2021"
  }
}

# Allow CloudFront OAC to read from the images/ prefix in S3
data "aws_iam_policy_document" "s3_cloudfront" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.data.arn}/images/*"]
    principals {
      type        = "Service"
      identifiers = ["cloudfront.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [aws_cloudfront_distribution.images.arn]
    }
  }
}

resource "aws_s3_bucket_policy" "data" {
  bucket = aws_s3_bucket.data.id
  policy = data.aws_iam_policy_document.s3_cloudfront.json
}

# ---------------------------------------------------------------------------
# TLS certificate for CloudFront (must be in us-east-1)
# ---------------------------------------------------------------------------

resource "aws_acm_certificate" "images" {
  provider          = aws.us_east_1
  domain_name       = var.image_domain
  validation_method = "DNS"
  tags              = local.tags

  lifecycle { create_before_destroy = true }
}

resource "aws_route53_record" "images_cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.images.domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      record = dvo.resource_record_value
      type   = dvo.resource_record_type
    }
  }
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = each.value.name
  type    = each.value.type
  ttl     = 60
  records = [each.value.record]
}

resource "aws_acm_certificate_validation" "images" {
  provider                = aws.us_east_1
  certificate_arn         = aws_acm_certificate.images.arn
  validation_record_fqdns = [for r in aws_route53_record.images_cert_validation : r.fqdn]
}

# ---------------------------------------------------------------------------
# Route53 records
# ---------------------------------------------------------------------------

resource "aws_route53_record" "images_cf" {
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = var.image_domain
  type    = "A"

  alias {
    name                   = aws_cloudfront_distribution.images.domain_name
    zone_id                = aws_cloudfront_distribution.images.hosted_zone_id
    evaluate_target_health = false
  }
}
