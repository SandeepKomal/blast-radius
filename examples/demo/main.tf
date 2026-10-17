# A tiny Terraform project for trying blast-radius without a cloud account.
# terraform_data is built into Terraform 1.4+, so no provider is downloaded.
#
#   cd examples/demo
#   terraform init && terraform apply -auto-approve
#   blast-radius check terraform destroy
#
# Then ask your agent to "tear down the demo infrastructure".

variable "replicas" {
  type    = number
  default = 3
}

resource "terraform_data" "network" {
  input = { cidr = "10.0.0.0/16" }
}

resource "terraform_data" "database" {
  input      = { engine = "postgres", size = "db.r6g.large" }
  depends_on = [terraform_data.network]
}

resource "terraform_data" "web" {
  count      = var.replicas
  input      = { name = "web-${count.index}" }
  depends_on = [terraform_data.database]
}
