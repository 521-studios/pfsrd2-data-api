terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
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

# Required to destroy the images ACM certificate (orphaned from old architecture).
# Remove this provider block once the cert is gone from state.
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
          "${aws_s3_bucket.data.arn}/images/*",
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
      IMAGE_DOMAIN     = var.app_domain
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
# Lambda Function URL — CloudFront uses this as the API origin via OAC
# ---------------------------------------------------------------------------

resource "aws_lambda_function_url" "api" {
  function_name      = aws_lambda_function.api.function_name
  authorization_type = "AWS_IAM"
}

# Grants CloudFront OAC permission to invoke this Function URL.
# Both actions are required: InvokeFunctionUrl (Function URL auth layer) and
# InvokeFunction (underlying Lambda invocation).  OAC signs requests with SigV4;
# the Lambda is not publicly accessible — only reachable through CloudFront.
resource "aws_lambda_permission" "cloudfront_url" {
  statement_id  = "AllowCloudFrontInvokeFunctionUrl"
  action        = "lambda:InvokeFunctionUrl"
  function_name = aws_lambda_function.api.function_name
  principal     = "cloudfront.amazonaws.com"
}

resource "aws_lambda_permission" "cloudfront_invoke" {
  statement_id  = "AllowCloudFrontInvokeFunction"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.api.function_name
  principal     = "cloudfront.amazonaws.com"
}
