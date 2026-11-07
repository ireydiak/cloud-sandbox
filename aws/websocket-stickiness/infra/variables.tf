variable "region" {
  type    = string
  default = "us-east-1"
}

variable "profile" {
  type    = string
  default = "default"
}

variable "image_tag" {
  type        = string
  default     = "latest"
  description = "Server image tag (the Makefile passes a content hash)."
}

variable "desired_count" {
  type    = number
  default = 3
}

variable "graceful_close" {
  type        = bool
  default     = false
  description = "Default for sockets that do not pass ?graceful=on|off."
}

variable "graceful_delay_seconds" {
  type        = number
  default     = 10
  description = "Seconds between SIGTERM and the server closing sockets with 1001."
}

variable "allowed_cidr" {
  type        = string
  default     = "0.0.0.0/0"
  description = "Who may reach the ALB on port 80. Narrow this to your IP/32 if you like."
}
