FROM scratch
COPY build/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
