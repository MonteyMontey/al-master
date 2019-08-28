#!/bin/bash

sudo docker save al-master | gzip > al-master.tar.gz &&
scp -i ./deploy_key -o StrictHostKeyChecking=no al-master.tar.gz montey@$1:/tmp &&
ssh -i ./deploy_key -o StrictHostKeyChecking=no montey@$1 << EOF
sudo systemctl stop master.service;
cat /tmp/al-master.tar.gz | gunzip | sudo docker load;
rm /tmp/al-master.tar.gz;
sudo systemctl enable master;
sudo docker system prune -f;
sudo reboot
EOF

ssh -i ./deploy_key -o StrictHostKeyChecking=no montey@$2 'sudo reboot' || true
ssh -i ./deploy_key -o StrictHostKeyChecking=no montey@$3 'sudo reboot' || true