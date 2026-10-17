# Records docs/demo.gif with https://github.com/charmbracelet/vhs.
#
# Needs blast-radius and terraform on PATH. Prepare the demo project first:
#
#   cd examples/demo
#   terraform init && terraform apply -auto-approve
#   terraform workspace new prod && terraform apply -auto-approve
#   terraform workspace select default
#   vhs ../../docs/demo.tape

Output ../../docs/demo.gif

Set Shell bash
Set FontSize 17
Set Width 1000
Set Height 330
Set Padding 20
Set TypingSpeed 40ms
Set Theme "Catppuccin Mocha"

Hide
Type "export PS1='$ ' && clear"
Enter
Sleep 500ms
Show

Type "terraform workspace show"
Enter
Sleep 800ms
Type "blast-radius check terraform destroy -auto-approve"
Enter
Sleep 4s

Hide
Type "clear"
Enter
Show

Type "terraform workspace select prod"
Enter
Sleep 800ms
Type "blast-radius check terraform destroy -auto-approve"
Enter
Sleep 4s

Hide
Type "clear"
Enter
Show

Type "blast-radius check 'terraform apply -var replicas=1 -auto-approve'"
Enter
Sleep 4500ms
