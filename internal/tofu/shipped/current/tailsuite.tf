# tailsuite's module (decision 0014): the logic. The values are in
# tailnet.auto.tfvars, beside it.
#
# Written by tailsuite. Don't edit it: a lab only runs a tailsuite.tf that
# a tailsuite release shipped, so the lab and production apply the same
# thing. `tailsuite upgrade tofu` replaces it with a newer release's copy
# and leaves the values alone.

terraform {
  required_providers {
    tailscale = {
      source  = "tailscale/tailscale"
      version = "0.29.2" # exact: the lab and production run the same provider
    }
  }
}

variable "devices" {
  description = "Each device's tags, by the device's name in the tailnet."
  type = map(object({
    tags = optional(list(string), [])
  }))
  default = {}
}

# The policy file is the only copy, beside tofu/. No input changes this
# path, so nothing can point the module at another policy.
resource "tailscale_acl" "policy" {
  acl = file("${path.module}/../policy.hujson")
}

# Devices are found by their short MagicDNS name, never by ID or by the
# tailnet's domain: the lab's devices and production's have different
# IDs and domains, and the same short names. A device's full name is
# "<short name>.<tailnet domain>", so its short name and a dot are a
# prefix no other device's name has. Devices shared in from other
# tailnets are listed too, and never count: they aren't this tailnet's.
data "tailscale_devices" "tagged" {
  for_each    = { for name, d in var.devices : name => d if length(d.tags) > 0 }
  name_prefix = "${each.key}."

  lifecycle {
    postcondition {
      condition     = length([for d in self.devices : d if !d.is_external]) == 1
      error_message = "Want exactly one device named ${each.key} in this tailnet; found ${length([for d in self.devices : d if !d.is_external])}."
    }
  }
}

resource "tailscale_device_tags" "tagged" {
  for_each  = data.tailscale_devices.tagged
  device_id = one([for d in each.value.devices : d if !d.is_external]).node_id
  tags      = var.devices[each.key].tags
}
