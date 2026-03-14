output "lambda_function_url" {
  description = "Lambda Function URL (used as CloudFront API origin)"
  value       = aws_lambda_function_url.api.function_url
}

output "lambda_function_name" {
  description = "Lambda function name"
  value       = aws_lambda_function.api.function_name
}

output "s3_bucket_name" {
  description = "pfsrd2-data S3 bucket name"
  value       = aws_s3_bucket.data.id
}

output "s3_bucket_arn" {
  description = "pfsrd2-data S3 bucket ARN"
  value       = aws_s3_bucket.data.arn
}

output "indexer_iam_policy_arn" {
  description = "IAM policy ARN for pfsrd2-parser (indexer) to write to S3"
  value       = aws_iam_policy.indexer_s3.arn
}

output "cloudfront_images_domain" {
  description = "CloudFront domain for image serving"
  value       = aws_cloudfront_distribution.images.domain_name
}
