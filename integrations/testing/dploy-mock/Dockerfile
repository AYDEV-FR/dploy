FROM python:3.11-slim

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY app.py .

EXPOSE 8080
# A couple of workers so concurrent panel polls don't queue behind each other.
CMD ["gunicorn", "-b", "0.0.0.0:8080", "-w", "2", "app:app"]
