# CaBrain auto-deploy — install on the stack host (once)

```bash
sudo apt-get install -y webhook
sudo mkdir -p /home/fadymondy/services/cabrain-deploy
git clone https://github.com/togo-framework/cabrain.git /home/fadymondy/services/cabrain-src
openssl rand -hex 32 | sudo tee /home/fadymondy/services/cabrain-deploy/.webhook-secret
sudo chmod 600 /home/fadymondy/services/cabrain-deploy/.webhook-secret
WHSECRET=$(sudo cat /home/fadymondy/services/cabrain-deploy/.webhook-secret)
sudo sed "s|@@WEBHOOK_SECRET@@|$WHSECRET|" \
  /home/fadymondy/services/cabrain-src/infra/deploy/hooks.yaml.template \
  > /home/fadymondy/services/cabrain-deploy/hooks.yaml
sudo cp /home/fadymondy/services/cabrain-src/infra/deploy/cabrain-webhook.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now cabrain-webhook
```

GitHub → repo Settings → Webhooks → Add webhook:
- URL: `https://deploy.fadymondy.com/hooks/cabrain-deploy`
- Content type: `application/json`
- Secret: the value in `/home/fadymondy/services/cabrain-deploy/.webhook-secret`
- Events: just the push event

On every push to `main`, `deploy.sh` runs: git pull → docker build → docker run.
