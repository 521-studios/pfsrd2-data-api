output "api_endpoint" {
  description = "API Gateway endpoint URL"
  value       = aws_apigatewayv2_api.api.api_endpoint
}

output "lambda_function_name" {
  description = "Lambda function name"
  value       = aws_lambda_function.api.function_name
}

output "s3_bucket" {
  description = "S3 data bucket name"
  value       = aws_s3_bucket.data.id
}

output "cloudfront_images_domain" {
  description = "CloudFront distribution domain for images"
  value       = aws_cloudfront_distribution.images.domain_name
}
