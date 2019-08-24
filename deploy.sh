#!/bin/bash

sudo docker save al-master | gzip > al-master.tar.gz &&
scp -i ./deploy_key -o StrictHostKeyChecking=no al-master.tar.gz montey@35.246.168.135:/tmp &&
ssh -i ./deploy_key -o StrictHostKeyChecking=no montey@35.246.168.135 << EOF
sudo systemctl stop master.service;
cat /tmp/al-master.tar.gz | gunzip | sudo docker load;
rm /tmp/al-master.tar.gz;
sudo systemctl enable master;
sudo systemctl restart master;
sudo docker system prune -f
EOF
