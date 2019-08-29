run:
	go run main/main.go

test:
	go test ./...

image:
	docker build -t al-master .

TAG = latest

docker-push-dev:
	echo "${DOCKER_PASSWORD}" | docker login -u "${DOCKER_USERNAME}" --password-stdin
	docker tag al-master monteymontey/al-master-dev:$(TAG)
	docker push monteymontey/al-master-dev:$(TAG)
docker-push:
	echo "${DOCKER_PASSWORD}" | docker login -u "${DOCKER_USERNAME}" --password-stdin
	docker tag al-master monteymontey/al-master:$(TAG)
	docker push monteymontey/al-master:$(TAG)

deploy:
	bash deploy.sh 35.228.90.154 35.233.115.56 35.197.192.211 

deploy-dev:
	bash deploy.sh 35.246.168.135 34.90.109.10 34.65.119.227