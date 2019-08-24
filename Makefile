run:
	go run main/main.go

test:
	go test ./...

image:
	docker build -t al-master .

docker-push-dev:
	echo "${DOCKER_PASSWORD}" | docker login -u "${DOCKER_USERNAME}" --password-stdin
	docker tag al-master monteymontey/al-master-dev:latest
	docker push monteymontey/al-master-dev:latest
docker-push:
	echo "${DOCKER_PASSWORD}" | docker login -u "${DOCKER_USERNAME}" --password-stdin
	docker tag al-master monteymontey/al-master:latest
	docker push monteymontey/al-master:latest

deploy:
	bash deploy.sh