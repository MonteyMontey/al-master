#!/bin/bash
# a script that:
# - (0) zips and uploads the new image to compute engine instance
# - (1) ssh into the instance
# - (2) replaces the old docker image 
# - (3) removes uploaded .tar.gz. file
# - (4) and restarts the master service

sudo docker save al-master | gzip > al-master.tar.gz &&
scp -i ./id_rsa -o StrictHostKeyChecking=no al-master.tar.gz montey@35.207.190.1:/tmp &&
ssh -i ./id_rsa -o StrictHostKeyChecking=no montey@35.207.190.1 << EOF
cat /tmp/al-master.tar.gz | gunzip | sudo docker load;
rm /tmp/al-master.tar.gz;
sudo systemctl restart master
EOF